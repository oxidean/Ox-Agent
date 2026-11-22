package appctx

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/oxidean/Ox-Agent/internal/state"
)

// startWithSpec creates a work item carrying an artifacts/spec.md.
func startWithSpec(t *testing.T, root, title, spec string) *state.WorkItem {
	t.Helper()
	item, err := state.Start(root, title)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	dir := filepath.Join(item.Dir, "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec.md: %v", err)
	}
	return item
}

// specEvents returns the journal's spec.* events for the work item.
func specEvents(t *testing.T, root, itemID string) []state.Event {
	t.Helper()
	events, err := state.EventsFor(root, itemID, 0)
	if err != nil {
		t.Fatalf("EventsFor() error = %v", err)
	}
	var out []state.Event
	for _, e := range events {
		if strings.HasPrefix(e.Type, "spec.") {
			out = append(out, e)
		}
	}
	return out
}

// TestPromoteCreatesSpec verifies promotion copies the spec into
// context/specs, refreshes the index, and journals spec.promoted.
func TestPromoteCreatesSpec(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "Rate limiting", "# rate limits\n\n10 rps.\n")
	rel, updated, err := Promote(root, item, "")
	if err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if updated {
		t.Fatal("first promotion reported updated=true")
	}
	if rel != "context/specs/rate-limiting.md" {
		t.Fatalf("promoted path = %q", rel)
	}
	data, err := os.ReadFile(filepath.Join(root, ".ox-agent", "context", "specs", "rate-limiting.md"))
	if err != nil {
		t.Fatalf("promoted spec missing: %v", err)
	}
	if string(data) != "# rate limits\n\n10 rps.\n" {
		t.Fatalf("promoted spec content = %q", data)
	}
	entries, err := Entries(root, "spec")
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Path == rel {
			found = true
		}
	}
	if !found {
		t.Fatal("promoted spec not in the index")
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 2 || events[0].Type != "spec.promoting" || events[1].Type != "spec.promoted" {
		t.Fatalf("spec events = %v", events)
	}
	if events[1].Data["spec"] != rel {
		t.Fatalf("promoted event spec = %v", events[1].Data["spec"])
	}
}

// TestPromoteUpdatesInPlace verifies re-promoting the same work item
// overwrites the spec of record and journals spec.updated.
func TestPromoteUpdatesInPlace(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "Retry budget", "v1\n")
	if _, _, err := Promote(root, item, ""); err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(item.Dir, "artifacts", "spec.md"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite spec: %v", err)
	}
	rel, updated, err := Promote(root, item, "")
	if err != nil {
		t.Fatalf("re-Promote() error = %v", err)
	}
	if !updated || rel != "context/specs/retry-budget.md" {
		t.Fatalf("re-promote = %q updated=%v", rel, updated)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".ox-agent", "context", "specs", "retry-budget.md"))
	if string(data) != "v2\n" {
		t.Fatalf("updated spec = %q", data)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 4 || events[2].Type != "spec.promoting" || events[3].Type != "spec.updated" {
		t.Fatalf("spec events = %v", events)
	}
}

// TestPromoteRefusesForeignSlug verifies a work item cannot promote onto a
// spec path a different work item created.
func TestPromoteRefusesForeignSlug(t *testing.T) {
	root := initProject(t)
	a := startWithSpec(t, root, "Shared title", "a\n")
	b := startWithSpec(t, root, "Shared title", "b\n")
	if _, _, err := Promote(root, a, ""); err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if _, _, err := Promote(root, b, ""); err == nil {
		t.Fatal("second work item promoted onto the first's spec path")
	}
}

func TestPromoteSerializesForeignOwners(t *testing.T) {
	root := initProject(t)
	items := []*state.WorkItem{
		startWithSpec(t, root, "Shared title", "a\n"),
		startWithSpec(t, root, "Shared title", "b\n"),
	}
	type result struct {
		item *state.WorkItem
		err  error
	}
	results := make(chan result, len(items))
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(item *state.WorkItem) {
			defer wg.Done()
			_, _, err := Promote(root, item, "")
			results <- result{item: item, err: err}
		}(item)
	}
	wg.Wait()
	close(results)

	var winner *state.WorkItem
	failures := 0
	for result := range results {
		if result.err == nil {
			winner = result.item
		} else {
			failures++
		}
	}
	if winner == nil || failures != 1 {
		t.Fatalf("promotion results: winner=%v failures=%d, want one winner and one refusal", winner, failures)
	}
	target := filepath.Join(root, ".ox-agent", "context", "specs", "shared-title.md")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read promoted spec: %v", err)
	}
	source, err := os.ReadFile(filepath.Join(winner.Dir, "artifacts", "spec.md"))
	if err != nil {
		t.Fatalf("read winning source: %v", err)
	}
	if string(data) != string(source) {
		t.Fatalf("promoted content = %q, want winner's %q", data, source)
	}
}

// TestPromoteRefusesUnpromotedFile verifies an existing spec file with no
// promotion provenance is never silently re-owned.
func TestPromoteRefusesUnpromotedFile(t *testing.T) {
	root := initProject(t)
	path := filepath.Join(root, ".ox-agent", "context", "specs", "migrated.md")
	if err := os.WriteFile(path, []byte("hand-maintained\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	item := startWithSpec(t, root, "whatever", "spec\n")
	if _, _, err := Promote(root, item, "migrated"); err == nil {
		t.Fatal("promote overwrote an unpromoted spec file")
	}
}

// TestPromoteMissingSpec verifies the error when the work item wrote no
// spec artifact.
func TestPromoteMissingSpec(t *testing.T) {
	root := initProject(t)
	item, err := state.Start(root, "no spec")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, _, err := Promote(root, item, ""); err == nil {
		t.Fatal("promote succeeded with no spec.md")
	}
}

// TestPromoteRejectsBadName verifies --name cannot escape context/specs.
func TestPromoteRejectsBadName(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "t", "spec\n")
	for _, name := range []string{"../escape", "a/b", "UPPER", "a_b", ".hidden", "con", "nul", strings.Repeat("a", 49)} {
		if _, _, err := Promote(root, item, name); err == nil {
			t.Fatalf("promote accepted name %q", name)
		}
	}
}

// TestIndexParsesCovers verifies front-matter `covers:` globs ride the
// index entries so collection can intersect them with artifacts.
func TestIndexParsesCovers(t *testing.T) {
	root := initProject(t)
	spec := "---\ncovers:\n  - internal/harness/**\n  - 'docs/*.md'\n---\n# api spec\n"
	path := filepath.Join(root, ".ox-agent", "context", "specs", "api.md")
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	index, err := Build(root)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, f := range index.Files {
		if f.Path == "context/specs/api.md" {
			if len(f.Covers) != 2 || f.Covers[0] != "internal/harness/**" || f.Covers[1] != "docs/*.md" {
				t.Fatalf("covers = %v", f.Covers)
			}
			return
		}
	}
	t.Fatal("spec not indexed")
}

// TestParseCovers exercises the front-matter fence rules.
func TestParseCovers(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want int
	}{
		{"no front matter", "# title\n", 0},
		{"mid-file fence", "intro\n---\ncovers:\n  - a/**\n---\n", 0},
		{"valid block", "---\ncovers:\n  - a/**\n  - b/**\n---\nbody\n", 2},
		{"ellipsis close", "---\ncovers: [a/**]\n...\nbody\n", 1},
		{"non-fence close", "---\ncovers: [a/**]\n--- trailing prose\n", 0},
		{"malformed yaml", "---\ncovers: [unclosed\n---\n", 0},
		{"unclosed front matter", "---\ncovers:\n  - a/**\n", 0},
		{"no covers key", "---\ntitle: x\n---\n", 0},
		{"empty entries dropped", "---\ncovers:\n  - ''\n  - a/**\n---\n", 1},
	} {
		got := parseCovers([]byte(tc.data))
		if len(got) != tc.want {
			t.Fatalf("%s: parseCovers len = %d (%v), want %d", tc.name, len(got), got, tc.want)
		}
	}
}

func TestCoveredSpecsRejectsNumericGlob(t *testing.T) {
	root := initProject(t)
	specPath := filepath.Join(root, ".ox-agent", "context", "specs", "api.md")
	if err := os.WriteFile(specPath, []byte("---\ncovers: [123]\n---\n# spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	specs, issues, err := CoveredSpecs(root)
	if err != nil {
		t.Fatalf("CoveredSpecs() error = %v", err)
	}
	if len(specs) != 0 || len(issues) != 1 {
		t.Fatalf("CoveredSpecs() = specs %v, issues %v; want invalid covers issue", specs, issues)
	}
}

// TestPromoteRecoversFailedIndexWrite verifies a durable promotion intent
// lets the same work item finish after the context index becomes writable.
func TestPromoteRecoversFailedIndexWrite(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "the spec", "# the spec\n")
	indexPath := filepath.Join(root, ".ox-agent", "context", "index.yaml")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(root, item, ""); err == nil {
		t.Fatal("Promote() succeeded while the index path was a directory")
	}
	target := filepath.Join(root, ".ox-agent", "context", "specs", "the-spec.md")
	if data, err := os.ReadFile(target); err != nil || string(data) != "# the spec\n" {
		t.Fatalf("prepared spec = %q, %v", data, err)
	}
	if events := specEvents(t, root, item.ID); len(events) != 1 || events[0].Type != "spec.promoting" {
		t.Fatalf("events after failed index write = %v", events)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	rel, updated, err := Promote(root, item, "")
	if err != nil {
		t.Fatalf("retry Promote() error = %v", err)
	}
	if rel != "context/specs/the-spec.md" {
		t.Fatalf("rel = %s", rel)
	}
	if updated {
		t.Fatal("recovery reported updated, want promoted")
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 3 || events[2].Type != "spec.promoted" {
		t.Fatalf("recovered events = %v", events)
	}
}

func TestPromoteRecoversEarlierPendingHashAfterChangedSource(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "the spec", "v3\n")
	specPath := "context/specs/the-spec.md"
	target := filepath.Join(root, ".ox-agent", filepath.FromSlash(specPath))
	if err := os.WriteFile(target, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, ".ox-agent", "context", "index.yaml")
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"v1\n", "v2\n"} {
		sum := sha256.Sum256([]byte(content))
		if err := state.Append(root, state.Event{
			Type:       "spec.promoting",
			WorkItemID: item.ID,
			Actor:      "test",
			Data: map[string]any{
				"spec":          specPath,
				"source_sha256": hex.EncodeToString(sum[:]),
				"updated":       false,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := Promote(root, item, ""); err == nil || !strings.Contains(err.Error(), "writing context index") {
		t.Fatalf("Promote() error = %v, want interrupted index write", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "v3\n" {
		t.Fatalf("recovered spec = %q, %v", data, err)
	}
}

func TestPromoteDoesNotOverwriteForeignFileAfterInterruptedPromotion(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "the spec", "# the spec\n")
	indexPath := filepath.Join(root, ".ox-agent", "context", "index.yaml")
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(root, item, ""); err == nil {
		t.Fatal("Promote() succeeded while the index path was a directory")
	}
	target := filepath.Join(root, ".ox-agent", "context", "specs", "the-spec.md")
	if err := os.WriteFile(target, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(indexPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(root, item, ""); err == nil {
		t.Fatal("retry overwrote a spec that no longer matches the promotion intent")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read foreign spec: %v", err)
	}
	if string(data) != "foreign\n" {
		t.Fatalf("foreign spec was changed to %q", data)
	}
}

func TestPromoteRecoversChangedSourceAfterUpdateFailure(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "the spec", "v1\n")
	if _, _, err := Promote(root, item, ""); err != nil {
		t.Fatalf("initial Promote() error = %v", err)
	}
	indexPath := filepath.Join(root, ".ox-agent", "context", "index.yaml")
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(item.Dir, "artifacts", "spec.md")
	if err := os.WriteFile(specPath, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(root, item, ""); err == nil {
		t.Fatal("update succeeded while the index path was a directory")
	}
	if err := os.WriteFile(specPath, []byte("v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	_, updated, err := Promote(root, item, "")
	if err != nil {
		t.Fatalf("recovery Promote() error = %v", err)
	}
	if !updated {
		t.Fatal("recovery reported updated=false")
	}
	promotedPath := filepath.Join(root, ".ox-agent", "context", "specs", "the-spec.md")
	if data, err := os.ReadFile(promotedPath); err != nil || string(data) != "v3\n" {
		t.Fatalf("recovered spec = %q, %v", data, err)
	}
	events := specEvents(t, root, item.ID)
	if len(events) != 5 || events[4].Type != "spec.updated" {
		t.Fatalf("recovered events = %v", events)
	}
}

func TestPromoteRefusesIdenticalUnpromotedFile(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "the spec", "# the spec\n")
	target := filepath.Join(root, ".ox-agent", "context", "specs", "the-spec.md")
	if err := os.WriteFile(target, []byte("# the spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(root, item, ""); err == nil {
		t.Fatal("Promote() claimed an identical file without a promotion intent")
	}
}

func TestPromoteStillRefusesForeignContent(t *testing.T) {
	root := initProject(t)
	item := startWithSpec(t, root, "the spec", "# the spec\n")
	target := filepath.Join(root, ".ox-agent", "context", "specs", "the-spec.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(root, item, ""); err == nil {
		t.Fatal("Promote() accepted foreign content")
	}
}
