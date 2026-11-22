package appctx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oxidean/Ox-Agent/internal/filelock"
	"github.com/oxidean/Ox-Agent/internal/state"
)

// Promote copies a work item's spec.md into context/specs/ as part of the
// project's durable spec trail, refreshes the context index, and journals
// the promotion. The promoted file is the spec of record for its subject:
// later work items receive it in their packet's context.specs and must
// reconcile against it. Re-promoting the same work item updates the file
// in place — the work item's own spec.md stays the point-in-time snapshot
// and the journal records every promotion.
//
// The name slug defaults to the work item's title; --name overrides it.
// A name already promoted from a different work item, or an existing spec
// file no promotion produced, refuses rather than silently re-owning it.
func Promote(root string, item *state.WorkItem, name string) (specPath string, updated bool, err error) {
	// Serialize promotions project-wide: the owner check → write → index →
	// journal sequence must be atomic or two concurrent promotes of the
	// same slug could both pass the provenance check and interleave.
	lockPath := filepath.Join(root, ".ox-agent", "runtime", "locks", "promote.lock")
	err = filelock.With(lockPath, filelock.DefaultTimeout, func() error {
		var lerr error
		specPath, updated, lerr = promote(root, item, name)
		return lerr
	})
	return specPath, updated, err
}

func promote(root string, item *state.WorkItem, name string) (specPath string, updated bool, err error) {
	source := filepath.Join(item.Dir, "artifacts", "spec.md")
	data, err := os.ReadFile(source)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, fmt.Errorf("%s has no artifacts/spec.md to promote", item.ID)
		}
		return "", false, err
	}
	slug := name
	if slug == "" {
		slug = specSlug(item.Title)
	}
	if len(slug) > 48 || !specSlugPattern.MatchString(slug) || dosDeviceStems[slug] {
		return "", false, fmt.Errorf("invalid spec name %q — use 1–48 lowercase letters, digits, and dashes (a-z0-9-), and not a Windows device name", slug)
	}
	rel := "context/specs/" + slug + ".md"
	target := filepath.Join(root, ".ox-agent", filepath.FromSlash(rel))
	exists := false
	if _, err := os.Stat(target); err != nil {
		if !os.IsNotExist(err) {
			return "", false, err
		}
	} else {
		exists = true
	}
	promotion, err := promotionState(root, rel)
	if err != nil {
		return "", false, err
	}
	switch {
	case promotion.owner == item.ID:
		exists = promotion.promoted || (promotion.pending && promotion.updated)
	case promotion.owner != "":
		return "", false, fmt.Errorf("%s is already promoted from %s — pass --name to pick a different spec name", rel, promotion.owner)
	case exists:
		return "", false, fmt.Errorf("%s already exists and was not promoted from a work item — pass --name to pick a different spec name", rel)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", false, err
	}
	relSource, err := filepath.Rel(root, source)
	if err != nil {
		return "", false, err
	}
	relSource = filepath.ToSlash(relSource)
	sum := sha256.Sum256(data)
	if err := state.Append(root, state.Event{
		Type:       "spec.promoting",
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data: map[string]any{
			"spec":          rel,
			"source":        relSource,
			"source_sha256": hex.EncodeToString(sum[:]),
			"updated":       exists,
		},
	}); err != nil {
		return "", false, fmt.Errorf("recording spec promotion intent: %w", err)
	}
	if err := writeSpecAtomically(target, data); err != nil {
		return "", false, err
	}
	// Rebuild under the shared index lock — promote.lock serializes
	// promotions, but `context index` must not overwrite this write
	// with a Build taken before the spec landed.
	if _, _, _, err := Rebuild(root); err != nil {
		return "", false, err
	}
	eventType := "spec.promoted"
	if exists {
		eventType = "spec.updated"
	}
	if err := state.Append(root, state.Event{
		Type:       eventType,
		WorkItemID: item.ID,
		Actor:      "ox-agent",
		Data:       map[string]any{"spec": rel, "source": relSource, "source_sha256": hex.EncodeToString(sum[:])},
	}); err != nil {
		return "", false, fmt.Errorf("spec promoted to %s but journaling %s failed: %w", rel, eventType, err)
	}
	return rel, exists, nil
}

func writeSpecAtomically(target string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(target), ".spec-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o644); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}

// HasPromotedSpec reports whether the journal ties any spec promotion to
// the work item — the archive hint's check for "spec left behind".
func HasPromotedSpec(root, workItemID string) (bool, error) {
	events, err := state.ReadEvents(root)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if (e.Type == "spec.promoted" || e.Type == "spec.updated") && e.WorkItemID == workItemID {
			return true, nil
		}
	}
	return false, nil
}

type promotionStatus struct {
	owner    string
	pending  bool
	promoted bool
	updated  bool
}

func promotionState(root, spec string) (promotionStatus, error) {
	events, err := state.ReadEvents(root)
	if err != nil {
		return promotionStatus{}, err
	}
	var status promotionStatus
	for _, e := range events {
		if e.Data["spec"] != spec {
			continue
		}
		switch e.Type {
		case "spec.promoting":
			if status.owner == "" || status.owner == e.WorkItemID {
				status.owner = e.WorkItemID
				status.pending = true
				status.updated, _ = e.Data["updated"].(bool)
			}
		case "spec.promoted":
			status.owner = e.WorkItemID
			status.pending = false
			status.promoted = true
			status.updated = false
		case "spec.updated":
			if status.owner != "" && status.owner != e.WorkItemID {
				continue
			}
			status.owner = e.WorkItemID
			status.pending = false
			status.promoted = true
			status.updated = true
		}
	}
	return status, nil
}

// specSlugPattern constrains promoted spec filenames so --name can never
// write outside context/specs or produce checkout-hostile names.
var specSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// dosDeviceStems are Windows device names a spec file must never claim —
// context/specs/con.md writes fine on POSIX and breaks git checkout on
// Windows.
var dosDeviceStems = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

var specNonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// specSlug derives a spec filename stem from a work-item title, matching
// the rules work-item directory slugs follow.
func specSlug(title string) string {
	slug := strings.Trim(specNonSlugChars.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	return slug
}
