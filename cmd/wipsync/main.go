package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ahaltindis/wipsync/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := cli.Root().ExecuteContext(ctx); err != nil {
		if ctx.Err() != nil {
			// Ctrl+C: nothing to report, exit the way a shell expects.
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "wipsync:", err)
		os.Exit(1)
	}
}
