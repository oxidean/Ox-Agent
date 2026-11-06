package harness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeIntegration plants an integration yaml under .ox-agent/integrations.
func writeIntegration(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, ".ox-agent", "integrations", name+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing integration %s: %v", name, err)
	}
}

// packetAt reads the posted packet the server received.
func packetAt(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var packet map[string]any
	if err := json.Unmarshal(body, &packet); err != nil {
		t.Fatalf("posted packet is not JSON: %v", err)
	}
	return packet
}

// TestWebhookAsyncLeavesRunPending verifies async mode posts the packet and
// leaves the run pending for the remote harness to write output.json back.
func TestWebhookAsyncLeavesRunPending(t *testing.T) {
	var got []byte
	var gotAuth, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	root, item := initProject(t)
	writeIntegration(t, root, "hook", "name: hook\nadapter: webhook\nendpoint: "+srv.URL+"\ntimeout_seconds: 10\n")
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "hook"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if run.Status != "pending" || run.Adapter != "webhook" {
		t.Fatalf("run = %+v", run)
	}
	packet := packetAt(t, got)
	if packet["work_item_id"] != item.ID {
		t.Fatalf("posted packet work_item_id = %v", packet["work_item_id"])
	}
	if gotType != "application/json" {
		t.Fatalf("content type = %q", gotType)
	}
	if gotAuth != "" {
		t.Fatalf("unexpected Authorization header %q", gotAuth)
	}
	if _, err := os.Stat(filepath.Join(run.Dir, "output.json")); !os.IsNotExist(err) {
		t.Fatalf("async dispatch should not write output.json")
	}
}

// TestWebhookSyncStoresResponse verifies sync mode stores the response body
// as output.json, ready for collect's schema validation.
func TestWebhookSyncStoresResponse(t *testing.T) {
	output := validOutput("specify")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(output)
	}))
	defer srv.Close()
	root, item := initProject(t)
	writeIntegration(t, root, "hook", "name: hook\nadapter: webhook\nmode: sync\nendpoint: "+srv.URL+"\n")
	run, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "hook"})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(run.Dir, "output.json"))
	if err != nil {
		t.Fatalf("sync dispatch wrote no output.json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("stored output is not JSON: %v", err)
	}
	if decoded["verdict"] != "pass" {
		t.Fatalf("stored output verdict = %v", decoded["verdict"])
	}
	run, collected, err := Collect(root, run.RunID)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if run.Status != "collected" || collected["verdict"] != "pass" {
		t.Fatalf("collected run = %s verdict %v", run.Status, collected["verdict"])
	}
}

// TestWebhookTokenFromEnv verifies token_env resolves the bearer token from
// the environment and sends it on the dispatch request.
func TestWebhookTokenFromEnv(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	root, item := initProject(t)
	t.Setenv("OX_HOOK_TOKEN", "s3cret")
	writeIntegration(t, root, "hook", "name: hook\nadapter: webhook\nendpoint: "+srv.URL+"\ntoken_env: OX_HOOK_TOKEN\n")
	if _, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "hook"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if gotAuth != "Bearer s3cret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

// TestWebhookMissingTokenFails verifies a configured token_env that is unset
// fails dispatch before any request leaves.
func TestWebhookMissingTokenFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent without a token")
	}))
	defer srv.Close()
	root, item := initProject(t)
	writeIntegration(t, root, "hook", "name: hook\nadapter: webhook\nendpoint: "+srv.URL+"\ntoken_env: OX_UNSET_TOKEN\n")
	_, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "hook"})
	if err == nil || !strings.Contains(err.Error(), "OX_UNSET_TOKEN") {
		t.Fatalf("Dispatch() error = %v", err)
	}
}

// TestWebhookNon2xxMarksRunFailed verifies an HTTP error response marks the
// run failed instead of leaving it pending.
func TestWebhookNon2xxMarksRunFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusBadGateway)
	}))
	defer srv.Close()
	root, item := initProject(t)
	writeIntegration(t, root, "hook", "name: hook\nadapter: webhook\nendpoint: "+srv.URL+"\n")
	if _, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "hook"}); err == nil {
		t.Fatal("Dispatch() on 502 succeeded; want error")
	}
	// Dispatch returns nil on adapter failure; find the persisted run record.
	entries, readErr := os.ReadDir(runsDir(root))
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("runs dir entries = %v err %v", entries, readErr)
	}
	run, loadErr := LoadRun(root, entries[0].Name())
	if loadErr != nil {
		t.Fatalf("LoadRun() error = %v", loadErr)
	}
	if run.Status != "failed" || !strings.Contains(run.Error, "502") {
		t.Fatalf("run = %s error %q", run.Status, run.Error)
	}
}

// TestWebhookRejectsBadConfig verifies endpoint and mode validation happen
// before dispatch.
func TestWebhookRejectsBadConfig(t *testing.T) {
	root, item := initProject(t)
	writeIntegration(t, root, "noep", "name: noep\nadapter: webhook\n")
	if _, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "noep"}); err == nil || !strings.Contains(err.Error(), "no endpoint") {
		t.Fatalf("missing endpoint error = %v", err)
	}
	writeIntegration(t, root, "badmode", "name: badmode\nadapter: webhook\nendpoint: http://127.0.0.1:1\nmode: carrier-pigeon\n")
	if _, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "badmode"}); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("bad mode error = %v", err)
	}
	writeIntegration(t, root, "badurl", "name: badurl\nadapter: webhook\nendpoint: file:///etc/passwd\n")
	if _, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "badurl"}); err == nil || !strings.Contains(err.Error(), "invalid endpoint") {
		t.Fatalf("bad endpoint error = %v", err)
	}
}

// TestWebhookRejectsCleartextHTTP verifies non-loopback http endpoints are
// refused — packets and bearer tokens must not travel in cleartext.
func TestWebhookRejectsCleartextHTTP(t *testing.T) {
	root, item := initProject(t)
	writeIntegration(t, root, "cleartext", "name: cleartext\nadapter: webhook\nendpoint: http://hooks.example.com/run\n")
	if _, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "cleartext"}); err == nil || !strings.Contains(err.Error(), "cleartext") {
		t.Fatalf("non-loopback http error = %v, want cleartext refusal", err)
	}
}

// TestWebhookBlocksRedirectToHTTP verifies an https→http redirect does not
// resend the packet and token in cleartext.
func TestWebhookBlocksRedirectToHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://hooks.example.com/capture", http.StatusFound)
	}))
	defer srv.Close()
	root, item := initProject(t)
	writeIntegration(t, root, "redirector", "name: redirector\nadapter: webhook\nendpoint: "+srv.URL+"\n")
	// httptest serves loopback http — the redirect target is the downgrade
	// the client must refuse.
	_, err := Dispatch(context.Background(), root, item, DispatchRequest{Skill: "specify", Stage: "specification", Integration: "redirector"})
	if err == nil || !strings.Contains(err.Error(), "cleartext") {
		t.Fatalf("redirect error = %v, want cleartext refusal", err)
	}
}
