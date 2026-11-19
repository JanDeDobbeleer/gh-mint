package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"github.com/JanDeDobbeleer/mint/internal/mint"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	err := mint.Run(ctx, os.Args[1:], version, os.Stdin, os.Stdout, os.Stderr)
	if err == nil {
		return 0
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if code := exitErr.ExitCode(); code > 0 {
			return code
		}
		return 1
	}

	fmt.Fprintln(os.Stderr, "mint:", err)
	return 1
}
