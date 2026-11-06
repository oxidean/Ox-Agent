package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
		return fmt.Errorf("integration %q has no endpoint", integration.Name)
	}
	u, err := url.Parse(integration.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("integration %q has invalid endpoint %q (must be http or https)", integration.Name, integration.Endpoint)
	}
	switch integration.Mode {
	case "", "async", "sync":
	default:
		return fmt.Errorf("integration %q has unknown mode %q (supported: async, sync)", integration.Name, integration.Mode)
	}
	token := ""
	if integration.TokenEnv != "" {
		token = os.Getenv(integration.TokenEnv)
		if token == "" {
			return fmt.Errorf("integration %q expects a bearer token in $%s, but it is not set", integration.Name, integration.TokenEnv)
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
	resp, err := http.DefaultClient.Do(req)
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
