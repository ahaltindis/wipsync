package main

import (
	"fmt"
	"os"

	"github.com/ahaltindis/wipsync/internal/cli"
)

func main() {
	if err := cli.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "wipsync:", err)
		os.Exit(1)
	}
}
