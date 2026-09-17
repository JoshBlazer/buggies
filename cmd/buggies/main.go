// Command buggies is the CLI. It is deliberately thin: every subcommand is a
// call into internal/pipeline with flags parsed here.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
)

const usage = `buggies — proof, not opinion

usage:
  buggies review  --repo <path> [--base <rev>] [--head <rev>]   review a diff, print findings
  buggies index   --repo <path>                                  build/update the code graph
  buggies version

Only reproduced findings are shown as findings. Everything else is discarded.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "version":
		fmt.Println("buggies v0.0.0-dev")
	case "review":
		err = fmt.Errorf("review: not implemented until M5")
	case "index":
		err = fmt.Errorf("index: not implemented until M3")
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	_ = ctx
	if err != nil {
		fmt.Fprintln(os.Stderr, "buggies:", err)
		os.Exit(1)
	}
}
