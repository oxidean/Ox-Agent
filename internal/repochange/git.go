package repochange

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

func CleanBaseline(root string) string {
	status, err := runGit(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil || len(status) > 0 {
		return ""
	}
	head, err := runGit(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	baseline := strings.TrimSpace(string(head))
	if !validBaseline(baseline) {
		return ""
	}
	return baseline
}

func ChangedPaths(root, baseline string) ([]string, error) {
	if !validBaseline(baseline) {
		return nil, fmt.Errorf("invalid Git baseline")
	}
	diff, err := runGit(root, "diff", "--no-ext-diff", "--name-status", "-z", "--find-renames", baseline, "--")
	if err != nil {
		return nil, fmt.Errorf("reading tracked changes since baseline: %w", err)
	}
	untracked, err := runGit(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("reading untracked changes: %w", err)
	}
	paths, err := parseNameStatus(diff)
	if err != nil {
		return nil, err
	}
	for _, entry := range bytes.Split(untracked, []byte{0}) {
		if len(entry) > 0 {
			paths = append(paths, string(entry))
		}
	}
	unique := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" || excluded(path) {
			continue
		}
		if _, ok := unique[path]; ok {
			continue
		}
		unique[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func parseNameStatus(data []byte) ([]string, error) {
	fields := bytes.Split(data, []byte{0})
	var paths []string
	for i := 0; i < len(fields); {
		if len(fields[i]) == 0 {
			i++
			continue
		}
		status := fields[i]
		i++
		count := 1
		if status[0] == 'R' || status[0] == 'C' {
			count = 2
		}
		if i+count > len(fields) || len(fields[i]) == 0 {
			return nil, fmt.Errorf("malformed Git name-status output")
		}
		for _, field := range fields[i : i+count] {
			if len(field) == 0 {
				return nil, fmt.Errorf("malformed Git name-status output")
			}
			paths = append(paths, string(field))
		}
		i += count
	}
	return paths, nil
}

func validBaseline(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func excluded(path string) bool {
	for _, prefix := range []string{".ox-agent/work/", ".ox-agent/state/", ".ox-agent/runtime/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func runGit(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
