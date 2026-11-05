// Ox-Agent is a repository-local AI-SDLC runtime distributed as a single
// Go binary. main only wires the process context into the command tree.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/oxidean/Ox-Agent/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Execute(ctx); err != nil {
		os.Exit(1)
	}
}
