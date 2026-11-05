package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WorkItem is a work item record under .ox-agent/work.
type WorkItem struct {
	ID    string
	Title string
	Dir   string
}

// workRoot returns the .ox-agent/work directory under root.
func workRoot(root string) string {
	return filepath.Join(root, ".ox-agent", "work")
}

// Start creates a work item in .ox-agent/work/active with the next
// available WI-NNNN identifier, writes its index.json, and records a
// work_item.created lifecycle event. ID allocation and directory creation
// are serialized across concurrent processes.
func Start(root, title string) (_ *WorkItem, err error) {
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("start requires a work-item title")
	}
	work := workRoot(root)
	var id, dir string
	created := false
	err = withWorkItemLock(work, func() error {
		next, err := nextWorkItemNumber(work)
		if err != nil {
			return err
		}
		id = fmt.Sprintf("WI-%04d", next)
		dir = filepath.Join(work, "active", id+"-"+slugify(title))
		if err := os.Mkdir(dir, 0o755); err != nil {
			return fmt.Errorf("creating work item: %w", err)
		}
		created = true
		now := time.Now().UTC().Format(time.RFC3339)
		index := map[string]any{"schema_version": 1, "id": id, "title": title, "state": "intake", "profile": "normal", "current_stage": "intake", "created_at": now}
		data, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding work item: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.json"), append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("writing work item: %w", err)
		}
		return nil
	})
	if err != nil {
		if created {
			if removeErr := os.RemoveAll(dir); removeErr != nil {
				return nil, fmt.Errorf("%w (rolling back %s also failed: %v)", err, dir, removeErr)
			}
		}
		return nil, err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		rel = dir
	}
	if err := Append(root, Event{
		Type:       "work_item.created",
		WorkItemID: id,
		Actor:      "cli",
		Data:       map[string]any{"title": title, "directory": filepath.ToSlash(rel)},
	}); err != nil {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			return nil, fmt.Errorf("%w (rolling back %s also failed: %v)", err, dir, removeErr)
		}
		return nil, err
	}
	return &WorkItem{ID: id, Title: title, Dir: dir}, nil
}

// ListActive returns the directory names of active work items.
func ListActive(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(workRoot(root), "active"))
	if err != nil {
		return nil, fmt.Errorf("reading active work items: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// nonSlugChars matches title characters that are not valid in a work-item slug.
var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a work-item title into a filesystem-safe directory suffix.
func slugify(title string) string {
	slug := strings.Trim(nonSlugChars.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		slug = "work-item"
	}
	return slug
}

// nextWorkItemNumber returns the lowest unallocated work-item number across
// the active and archived buckets.
func nextWorkItemNumber(workRoot string) (int, error) {
	highest := 0
	for _, bucket := range []string{"active", "archived"} {
		entries, err := os.ReadDir(filepath.Join(workRoot, bucket))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return 0, fmt.Errorf("reading %s work items: %w", bucket, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "WI-") {
				continue
			}
			digits := name[3:]
			if i := strings.IndexByte(digits, '-'); i >= 0 {
				digits = digits[:i]
			}
			if n, err := strconv.Atoi(digits); err == nil && n > highest {
				highest = n
			}
		}
	}
	return highest + 1, nil
}

// allocationLockStaleAfter bounds how long an orphaned allocation lock file is
// trusted before a waiting process reclaims it.
const allocationLockStaleAfter = 30 * time.Second

// withWorkItemLock serializes work-item ID allocation across concurrent
// ox-agent processes. The scan-and-create critical section runs while holding
// an atomic lock file so two start commands cannot claim the same WI-NNNN
// number under different directory slugs.
func withWorkItemLock(workRoot string, fn func() error) error {
	lockPath := filepath.Join(workRoot, ".allocate.lock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(lock, "pid=%d\n", os.Getpid())
			lock.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquiring work item lock: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > allocationLockStaleAfter {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for work item allocation lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
