package appctx

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// specFrontMatter models the YAML front-matter block a context file may
// open with. Only the fields the runtime reads are decoded; everything
// else passes through untouched.
type specFrontMatter struct {
	Covers []string `yaml:"covers"`
}

// frontMatterBlock returns the raw YAML inside a file's opening
// front-matter fence, or false when the file does not open with `---`.
// The block must close with a `---` or `...` line; an unclosed fence
// reads as ordinary content, not metadata.
func frontMatterBlock(data []byte) (block string, opened, closed bool) {
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", false, false
	}
	var b strings.Builder
	for _, line := range strings.Split(text, "\n")[1:] {
		line = strings.TrimRight(line, "\r")
		if line == "---" || line == "..." {
			return b.String(), true, true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), true, false
}

// parseCovers returns the `covers:` path globs declared in a file's YAML
// front matter, or nil when the file has no front-matter block. The block
// must open on the file's first line as `---` and close with a `---` or
// `...` line — anything else reads as ordinary content, not metadata.
// A malformed block yields no covers; collection diagnoses invalid covers.
func parseCovers(data []byte) []string {
	block, opened, closed := frontMatterBlock(data)
	if !opened || !closed {
		return nil
	}
	var fm specFrontMatter
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return nil
	}
	var covers []string
	for _, glob := range fm.Covers {
		if glob = strings.TrimSpace(glob); glob != "" {
			covers = append(covers, glob)
		}
	}
	return covers
}

// coversInvalid reports whether front matter declares a malformed
// `covers` value or a value that contains no usable globs.
func coversInvalid(data []byte) bool {
	block, opened, closed := frontMatterBlock(data)
	if !opened {
		return false
	}
	if !closed {
		return hasCoversKey(block)
	}
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return hasCoversKey(block)
	}
	raw, has := fm["covers"]
	if !has {
		return false
	}
	values, ok := raw.([]any)
	if !ok {
		return true
	}
	if len(values) == 0 {
		return false
	}
	valid := false
	for _, value := range values {
		glob, ok := value.(string)
		if !ok {
			return true
		}
		if strings.TrimSpace(glob) != "" {
			valid = true
		}
	}
	return !valid
}

func hasCoversKey(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "covers:") {
			return true
		}
	}
	return false
}
