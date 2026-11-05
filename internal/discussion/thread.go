// Package discussion manages durable discussion threads under
// .ox-agent/discussions/, per discussion.schema.json.
package discussion

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Message is one appended entry in a discussion thread.
type Message struct {
	At     string `yaml:"at" json:"at"`
	Author string `yaml:"author" json:"author"`
	Body   string `yaml:"body" json:"body"`
	SHA256 string `yaml:"sha256" json:"sha256"`
}

// Thread mirrors discussion.schema.json plus its append-only message log.
type Thread struct {
	SchemaVersion      int       `yaml:"schema_version" json:"schema_version"`
	ThreadID           string    `yaml:"thread_id" json:"thread_id"`
	SummaryVersion     int       `yaml:"summary_version" json:"summary_version"`
	Status             string    `yaml:"status" json:"status"`
	SourceHash         string    `yaml:"source_hash" json:"source_hash"`
	SummarizedMessages []string  `yaml:"summarized_messages" json:"summarized_messages"`
	Conclusion         string    `yaml:"conclusion,omitempty" json:"conclusion,omitempty"`
	Decisions          []string  `yaml:"decisions,omitempty" json:"decisions,omitempty"`
	OpenQuestions      []string  `yaml:"open_questions,omitempty" json:"open_questions,omitempty"`
	Title              string    `yaml:"title,omitempty" json:"title,omitempty"`
	Messages           []Message `yaml:"messages" json:"messages"`
}

// dir returns the discussions directory under root.
func dir(root string) string {
	return filepath.Join(root, ".ox-agent", "discussions")
}

// path returns the thread file path for an id.
func path(root, id string) string {
	return filepath.Join(dir(root), id+".yaml")
}

// Open creates a new discussion thread and returns it.
func Open(root, title, author string) (*Thread, error) {
	id := fmt.Sprintf("d-%03d", nextID(root))
	thread := &Thread{
		SchemaVersion:  1,
		ThreadID:       id,
		SummaryVersion: 1,
		Status:         "active",
		SourceHash:     "sha256:" + emptyHash(),
		Title:          title,
	}
	thread.Messages = append(thread.Messages, Message{
		At:     now(),
		Author: author,
		Body:   title,
		SHA256: hash(title),
	})
	thread.SourceHash = "sha256:" + hashMessages(thread.Messages)
	if err := os.MkdirAll(dir(root), 0o755); err != nil {
		return nil, err
	}
	return thread, thread.save(root)
}

// nextID returns the lowest free discussion number.
func nextID(root string) int {
	entries, err := os.ReadDir(dir(root))
	if err != nil {
		return 1
	}
	highest := 0
	for _, entry := range entries {
		var n int
		if _, err := fmt.Sscanf(entry.Name(), "d-%03d.yaml", &n); err == nil && n > highest {
			highest = n
		}
	}
	return highest + 1
}

// Load reads a thread by id (with or without the d- prefix).
func Load(root, id string) (*Thread, error) {
	if id == "" {
		return nil, fmt.Errorf("discussion id is required")
	}
	if !strings.HasPrefix(id, "d-") {
		id = "d-" + id
	}
	data, err := os.ReadFile(path(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no discussion %q", id)
		}
		return nil, err
	}
	var thread Thread
	if err := yaml.Unmarshal(data, &thread); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", id, err)
	}
	return &thread, nil
}

// List returns all threads, newest id last.
func List(root string) ([]*Thread, error) {
	entries, err := os.ReadDir(dir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Thread
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		thread, err := Load(root, entry.Name()[:len(entry.Name())-5])
		if err != nil {
			continue
		}
		out = append(out, thread)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ThreadID < out[j].ThreadID })
	return out, nil
}

// Add appends a message to an open thread.
func (t *Thread) Add(root, author, body string) error {
	if t.Status != "active" {
		return fmt.Errorf("discussion %s is %s; cannot append", t.ThreadID, t.Status)
	}
	t.Messages = append(t.Messages, Message{At: now(), Author: author, Body: body, SHA256: hash(body)})
	t.SourceHash = "sha256:" + hashMessages(t.Messages)
	return t.save(root)
}

// Summarize records a compacted summary and bumps the summary version,
// marking older messages as covered by it.
func (t *Thread) Summarize(root, conclusion string, decisions, questions []string) error {
	t.SummaryVersion++
	t.Conclusion = conclusion
	t.Decisions = decisions
	t.OpenQuestions = questions
	var hashes []string
	for _, m := range t.Messages {
		hashes = append(hashes, m.SHA256)
	}
	t.SummarizedMessages = hashes
	return t.save(root)
}

// Close marks a thread resolved and records the final summary.
func (t *Thread) Close(root, conclusion string) error {
	t.Status = "resolved"
	if conclusion != "" {
		t.Conclusion = conclusion
	}
	var hashes []string
	for _, m := range t.Messages {
		hashes = append(hashes, m.SHA256)
	}
	t.SummarizedMessages = hashes
	return t.save(root)
}

// save serializes the thread to disk.
func (t *Thread) save(root string) error {
	data, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("encoding discussion: %w", err)
	}
	return os.WriteFile(path(root, t.ThreadID), data, 0o644)
}

// now returns the current UTC timestamp.
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// hash returns the bare sha256 hex of a string.
func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// emptyHash is the sha256 of the empty string.
func emptyHash() string {
	return hash("")
}

// hashMessages folds message hashes into one source hash.
func hashMessages(messages []Message) string {
	combined := ""
	for _, m := range messages {
		combined += m.SHA256
	}
	return hash(combined)
}
