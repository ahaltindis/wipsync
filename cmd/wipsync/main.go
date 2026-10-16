package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ahaltindis/wipsync/internal/cli"
)

// version is set by the release build; see .github/workflows/release.yml.
var version = "dev"

func main() {
	cli.Version = version
	// SIGTERM is how a service is stopped.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The first Ctrl+C ends the command cleanly. After it, stop catching the
	// signal, so a second one ends the program at once.
	go func() {
		<-ctx.Done()
		stop()
	}()
	if err := cli.Root().ExecuteContext(ctx); err != nil {
		if ctx.Err() != nil {
			// Ctrl+C: nothing to report, exit the way a shell expects.
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "wipsync:", err)
		os.Exit(1)
	}
}
