package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// webhook dispatches the run's packet to an HTTP endpoint — transport only;
// the packet contract stays the interface. In async mode (the default) a
// 2xx acknowledgement leaves the run pending and the remote harness writes
// output.json back through the same file contract. In sync mode a non-empty
// 2xx response body is stored as output.json for collection; the skill
// output schema still validates it at collect time, so the transport never
// bypasses the contract. A bearer token may be supplied through the
// environment variable named by token_env — never in the yaml itself.
func (r *Run) webhook(ctx context.Context, root string, integration *Integration) error {
	if integration.Endpoint == "" {
		return r.fail(root, fmt.Sprintf("integration %q has no endpoint", integration.Name))
	}
	u, err := url.Parse(integration.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return r.fail(root, fmt.Sprintf("integration %q has invalid endpoint %q (must be http or https)", integration.Name, integration.Endpoint))
	}
	// Cleartext transport carries the packet — and any bearer token — to
	// network observers. Restrict http to loopback endpoints; everything
	// else must be https.
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return r.fail(root, fmt.Sprintf("integration %q endpoint %q uses cleartext http — only loopback http is allowed (use https)", integration.Name, integration.Endpoint))
	}
	switch integration.Mode {
	case "", "async", "sync":
	default:
		return r.fail(root, fmt.Sprintf("integration %q has unknown mode %q (supported: async, sync)", integration.Name, integration.Mode))
	}
	token := ""
	if integration.TokenEnv != "" {
		token = os.Getenv(integration.TokenEnv)
		if token == "" {
			return r.fail(root, fmt.Sprintf("integration %q expects a bearer token in $%s, but it is not set", integration.Name, integration.TokenEnv))
		}
	}
	timeout := time.Duration(integration.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	packet, err := os.ReadFile(filepath.Join(r.Dir, "packet.json"))
	if err != nil {
		return fmt.Errorf("reading packet: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, integration.Endpoint, bytes.NewReader(packet))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// The transport is built to keep cleartext packets on-host even when
	// the environment or resolver disagrees:
	//   - http requests never ride a proxy — a non-loopback HTTP_PROXY
	//     would receive the packet and bearer token in cleartext.
	//   - loopback-named hosts dial a literal loopback address, so a
	//     *.localhost record pointing elsewhere cannot leak the packet.
	//   - https keeps normal proxy and dial behavior.
	// Redirects that downgrade https→http would resend the packet in
	// cleartext — refuse them outright.
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(req *http.Request) (*url.URL, error) {
				if req.URL.Scheme == "http" {
					return nil, nil
				}
				return http.ProxyFromEnvironment(req)
			},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				dialer := &net.Dialer{Timeout: 30 * time.Second}
				if !isLoopback(host) {
					return dialer.DialContext(ctx, network, addr)
				}
				ip, err := loopbackIP(ctx, host)
				if err != nil {
					return nil, err
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme == "http" && !isLoopback(req.URL.Hostname()) {
				return fmt.Errorf("refusing redirect to cleartext http endpoint %s", req.URL)
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		r.Status = "failed"
		r.Error = fmt.Sprintf("webhook dispatch failed: %v", err)
		_ = r.save(root)
		return fmt.Errorf("webhook dispatch to %s failed: %w", integration.Endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		r.Status = "failed"
		r.Error = fmt.Sprintf("reading webhook response: %v", err)
		_ = r.save(root)
		return fmt.Errorf("reading webhook response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		r.Status = "failed"
		r.Error = fmt.Sprintf("webhook returned %s", resp.Status)
		_ = r.save(root)
		return fmt.Errorf("webhook %s returned %s: %s", integration.Name, resp.Status, truncate(body, 200))
	}
	// Sync mode stores the response body as the output document; an empty
	// body is treated as an async acknowledgement. Collect validates either
	// way, so a malformed response surfaces as a contract failure.
	if integration.Mode == "sync" && len(bytes.TrimSpace(body)) > 0 {
		if err := os.WriteFile(filepath.Join(r.Dir, "output.json"), body, 0o644); err != nil {
			return fmt.Errorf("writing webhook output: %w", err)
		}
	}
	return nil
}

// truncate shortens s for error messages.
func truncate(s []byte, n int) string {
	if len(s) <= n {
		return string(s)
	}
	return string(s[:n]) + "..."
}

// fail marks the run failed, records why, and returns the error — a
// refused dispatch must not leave the run pending: the pipeline can only
// retry the step once the run is terminal.
func (r *Run) fail(root, msg string) error {
	r.Status = "failed"
	r.Error = msg
	if err := r.save(root); err != nil {
		return fmt.Errorf("%s (also failed to record run failure: %v)", msg, err)
	}
	return fmt.Errorf("%s", msg)
}

// isLoopback reports whether host names a local endpoint — a localhost
// name or a loopback IP (including zoned IPv6 like ::1%%lo0) — the only
// place cleartext http is acceptable.
func isLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.IsLoopback()
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// loopbackIP resolves a loopback-named host to a literal loopback
// address for dialing. A name that resolves to no loopback address is
// rejected — the endpoint only passed isLoopback by name, so the
// resolver must not be allowed to route the packet off-host.
func loopbackIP(ctx context.Context, host string) (net.IP, error) {
	target := host
	if i := strings.IndexByte(target, '%'); i >= 0 {
		target = target[:i] // zoned literals keep the numeric form
	}
	if ip := net.ParseIP(target); ip != nil {
		if !ip.IsLoopback() {
			return nil, fmt.Errorf("endpoint %s is not a loopback address", host)
		}
		return ip, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", target)
	if err != nil {
		return nil, fmt.Errorf("resolving loopback endpoint %s: %w", host, err)
	}
	for _, ip := range ips {
		if ip.IsLoopback() {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("endpoint %s does not resolve to a loopback address", host)
}
