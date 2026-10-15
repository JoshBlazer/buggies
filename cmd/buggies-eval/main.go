// Command buggies-eval is Buggies' precision/recall harness. It runs the real
// review pipeline over each seeded-bug fixture under a fixtures directory and
// reports true positives (seeded bug reproduced), false positives (noise), and
// false negatives (missed), as precision/recall/F1.
//
// It drives the same pipeline as `buggies review`, so it needs a container
// runtime for the sandbox reproduction: gVisor (runsc) in CI, or runc locally
// with --allow-runc. Model access comes from ANTHROPIC_API_KEY or an
// `ant auth login` profile, exactly as the review command.
//
// Exit status is 0 only when every scored fixture passed (no misses, no
// noise), so it can gate CI on the "high precision, low noise" claim.
//
//	buggies-eval [--fixtures testdata/fixtures] [--only name] [--json]
//	             [--data <dir>] [--allow-runc] [--skills-off] [--quiet]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/joshblazer/buggies/internal/eval"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fs := flag.NewFlagSet("buggies-eval", flag.ContinueOnError)
	fixtures := fs.String("fixtures", "testdata/fixtures", "directory of seeded-bug fixtures")
	only := fs.String("only", "", "run only the fixture with this name")
	asJSON := fs.Bool("json", false, "emit the machine-readable report as JSON")
	data := fs.String("data", "", "scratch data directory (default: a temp dir, removed on exit)")
	allowRunc := fs.Bool("allow-runc", os.Getenv("BUGGIES_ALLOW_RUNC") == "1", "run the sandbox on runc instead of gVisor (development only)")
	skillsOff := fs.Bool("skills-off", os.Getenv("BUGGIES_SKILLS") == "off", "disable the agent skill pack")
	quiet := fs.Bool("quiet", false, "suppress the pipeline's per-candidate progress log")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if err := run(ctx, *fixtures, *only, *asJSON, *data, *allowRunc, *skillsOff, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, "buggies-eval:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, fixtures, only string, asJSON bool, data string, allowRunc, skillsOff, quiet bool) error {
	fxs, err := eval.Load(fixtures)
	if err != nil {
		return err
	}
	if only != "" {
		kept := fxs[:0]
		for _, fx := range fxs {
			if fx.Name == only {
				kept = append(kept, fx)
			}
		}
		fxs = kept
		if len(fxs) == 0 {
			return fmt.Errorf("no fixture named %q under %s", only, fixtures)
		}
	}
	if len(fxs) == 0 {
		return fmt.Errorf("no fixtures found under %s", fixtures)
	}

	dataDir := data
	if dataDir == "" {
		dataDir, err = os.MkdirTemp("", "buggies-eval-data-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dataDir)
	} else if dataDir, err = filepath.Abs(dataDir); err != nil {
		return err
	}

	var logw io.Writer
	if !quiet {
		logw = os.Stderr
	}
	opts := eval.Options{DataDir: dataDir, AllowRunc: allowRunc, SkillsOff: skillsOff, Log: logw}

	rep, err := eval.Run(ctx, fxs, opts)
	if err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		eval.WriteText(os.Stdout, rep)
	}
	if !rep.Pass {
		os.Exit(1)
	}
	return nil
}
