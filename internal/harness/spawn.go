package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// spawn runs a spawn-cli integration's command template with the packet and
// output paths substituted, then leaves the run pending for collection when
// the command produced no output yet.
func (r *Run) spawn(ctx context.Context, root string, integration *Integration) error {
	if integration.Command == "" {
		return fmt.Errorf("integration %q has no command template", integration.Name)
	}
	timeout := time.Duration(integration.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	replacer := strings.NewReplacer(
		"{packet}", filepath.Join(r.Dir, "packet.json"),
		"{output}", filepath.Join(r.Dir, "output.json"),
		"{run_dir}", r.Dir,
		"{work_item}", r.WorkItemID,
		"{skill}", r.Skill,
		"{stage}", r.Stage,
	)
	command := replacer.Replace(integration.Command)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	proc := exec.CommandContext(ctx, "sh", "-c", command)
	proc.Dir = root
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	if err := proc.Run(); err != nil {
		r.Status = "failed"
		r.Error = fmt.Sprintf("spawn-cli command failed: %v", err)
		_ = r.save(root)
		return fmt.Errorf("spawn-cli command failed: %w", err)
	}
	return nil
}
