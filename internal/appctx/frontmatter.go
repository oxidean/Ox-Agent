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

// parseCovers returns the `covers:` path globs declared in a file's YAML
// front matter, or nil when the file has no front-matter block. The block
// must open on the file's first line as `---` and close with a `---` or
// `...` line — anything else reads as ordinary content, not metadata.
// A malformed block yields no covers rather than an error: the file still
// indexes, and doctor's schema checks own content validation.
func parseCovers(data []byte) []string {
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return nil
	}
	var block strings.Builder
	for _, line := range strings.Split(text, "\n")[1:] {
		line = strings.TrimRight(line, "\r")
		if line == "---" || line == "..." {
			var fm specFrontMatter
			if err := yaml.Unmarshal([]byte(block.String()), &fm); err != nil {
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
		block.WriteString(line)
		block.WriteByte('\n')
	}
	return nil
}
