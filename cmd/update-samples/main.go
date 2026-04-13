// Command update-samples updates pinned versions in samples/base/Dockerfile
// by reusing the internal/update probing and rewriting infrastructure. It is a
// developer tool invoked via `make update-samples` before a release.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/woditschka/confine-ai/internal/update"
)

func main() {
	os.Exit(main1())
}

func main1() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := update.NewClient("dev")
	prober := update.NewRealProber(client)
	return run(ctx, "samples/base/Dockerfile", prober, os.Stdout, os.Stderr)
}

// run executes the update-samples workflow: parse, classify, probe, and
// rewrite. It returns an exit code (0 on success, non-zero on failure).
func run(ctx context.Context, path string, prober update.Prober, stdout, stderr io.Writer) int {
	// 1. Read the sample Dockerfile.
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	// 2. Parse.
	pd := update.ParseDockerfile(content)
	if pd.MultiStage {
		fmt.Fprintf(stderr, "error: %s has multiple FROM stages; only single-stage files are supported\n", path)
		return 1
	}
	for _, w := range pd.Warnings {
		fmt.Fprintf(stderr, "warning: line %d: %s\n", w.LineNumber, w.Message)
	}

	// 3. Classify.
	targets, err := update.Classify(pd)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if len(targets) == 0 {
		fmt.Fprintf(stdout, "%s: unchanged\n", path)
		return 0
	}

	// 4. Probe each target.
	type outcome struct {
		target   update.UpdateTarget
		resolved update.Resolved
		err      error
	}
	outcomes := make([]outcome, 0, len(targets))
	for _, tgt := range targets {
		var resolved update.Resolved
		var probeErr error
		switch tgt.Tool {
		case "go":
			resolved, probeErr = prober.ProbeGo(ctx, tgt.Arches)
		case "java":
			resolved, probeErr = prober.ProbeCorretto(ctx, tgt.CurrentVersion, tgt.Arches)
		default:
			continue
		}
		outcomes = append(outcomes, outcome{target: tgt, resolved: resolved, err: probeErr})
	}

	// 5. Check for probe failures. Any failure halts the run atomically.
	for _, o := range outcomes {
		if o.err != nil {
			fmt.Fprintf(stderr, "error: %s probe failed: %v\n", o.target.Tool, o.err)
			return 1
		}
	}

	// 6. Build delta and detect changes.
	delta := update.Delta{}
	anyChange := false
	for _, o := range outcomes {
		if versionOrShasChanged(o.target, o.resolved, pd) {
			delta[o.target.Group] = o.resolved
			anyChange = true
		}
	}

	if !anyChange {
		fmt.Fprintf(stdout, "%s: unchanged\n", path)
		return 0
	}

	// 7. Rewrite atomically.
	newBytes := update.Rewrite(pd, delta)
	if err := update.WriteAtomic(path, newBytes, 0); err != nil {
		fmt.Fprintf(stderr, "error: write %s: %v\n", path, err)
		return 1
	}

	// 8. Print per-group summary.
	for _, o := range outcomes {
		name := o.target.Tool
		if o.target.Distribution != "" {
			name = fmt.Sprintf("%s/%s", o.target.Tool, o.target.Distribution)
		}
		if o.target.CurrentVersion == o.resolved.Version && !versionOrShasChanged(o.target, o.resolved, pd) {
			fmt.Fprintf(stdout, "%s: %s %s (unchanged)\n", path, name, o.resolved.Version)
		} else {
			fmt.Fprintf(stdout, "%s: %s %s -> %s\n", path, name, o.target.CurrentVersion, o.resolved.Version)
		}
	}

	return 0
}

// versionOrShasChanged reports whether the probed version or any sha256 value
// differs from what the parsed Dockerfile currently holds.
func versionOrShasChanged(tgt update.UpdateTarget, resolved update.Resolved, pd *update.ParsedDockerfile) bool {
	g := tgt.Group
	if g == nil {
		return false
	}
	currentVersion := argValue(pd, g.VersionLineIdx)
	if currentVersion != resolved.Version {
		return true
	}
	for arch, idx := range g.Sha256LinesByArch {
		want, ok := resolved.Sha256[arch]
		if !ok {
			continue
		}
		if argValue(pd, idx) != want {
			return true
		}
	}
	return false
}

// argValue extracts the ARG value string at the given line index.
func argValue(pd *update.ParsedDockerfile, idx int) string {
	if idx < 0 || idx >= len(pd.Lines) {
		return ""
	}
	line := pd.Lines[idx]
	return string(line.Raw[line.ArgValueStart:line.ArgValueEnd])
}
