// circuitctl is the only path bytes and metadata enter the go-zk-circuits
// catalog (spec §5.1, §5.3) — there is no upload API and no hand-edited
// manifest.json; CI runs `circuitctl verify` to enforce that.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirosfoundation/go-zk-circuits/pkg/publish"
)

var version = "0.1.0-dev" // set at build time via -ldflags, mirroring cmd/zkc

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "add":
		err = runAdd(args)
	case "verify":
		err = runVerify(args)
	case "ls":
		err = runLs(args)
	case "deprecate":
		err = runLifecycle(args, "deprecate")
	case "revoke":
		err = runLifecycle(args, "revoke")
	case "publish":
		err = runLifecycle(args, "publish")
	case "unpublish":
		err = runLifecycle(args, "unpublish")
	case "-h", "--help", "help":
		usage()
		return
	case "-v", "--version", "version":
		fmt.Println("circuitctl", version)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `circuitctl — publish and manage the zk-circuits catalog

Usage:
  circuitctl add <file> --system <system> --origin <url> [--unpublished] [options]
  circuitctl verify [--root <path>] [--generated-at <RFC3339>]
  circuitctl ls [--root <path>] [--stale]
  circuitctl deprecate <id> --reason <text> [--root <path>]
  circuitctl revoke <id> --reason <text> [--root <path>]
  circuitctl publish <id> [--reason <text>] [--root <path>]
  circuitctl unpublish <id> [--reason <text>] [--root <path>]

See circuit-distribution-service-spec.md §5.3 and §2.4.1 for full semantics.`)
}

type repeatedFlag []string

func (r *repeatedFlag) String() string     { return strings.Join(*r, ",") }
func (r *repeatedFlag) Set(v string) error { *r = append(*r, v); return nil }

// splitLeadingPositional pulls a single positional argument out of args
// regardless of where it appears, so the documented usage in spec §5.3 —
// e.g. "circuitctl add <file> --system longfellow ..." with the positional
// argument BEFORE the flags — actually works. Go's flag package stops
// scanning at the first non-flag token, so without this, everything after
// <file> would silently be treated as extra positional args instead of flags.
//
// boolFlags must list every flag (without leading dashes) that takes NO
// value — e.g. "unpublished", "open-source". Getting this wrong is a real
// bug, not a theoretical one: without it, a boolean flag greedily consumes
// the NEXT flag's name as its own "value", which then strands that next
// flag's actual value as an orphaned bare token — exactly the failure mode
// this function exists to avoid, just relocated to whichever flag comes
// after the boolean one.
func splitLeadingPositional(args []string, boolFlags map[string]bool) (positional string, rest []string, err error) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			isBool := boolFlags[name]
			if !isBool && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
			continue
		}
		positionals = append(positionals, a)
	}
	if len(positionals) != 1 {
		return "", nil, fmt.Errorf("expected exactly one positional argument, got %d", len(positionals))
	}
	return positionals[0], rest, nil
}

func runAdd(args []string) error {
	file, flagArgs, err := splitLeadingPositional(args, map[string]bool{"unpublished": true, "open-source": true})
	if err != nil {
		return fmt.Errorf("%w (usage: circuitctl add <file> --system <system> --origin <url> [options])", err)
	}
	args = flagArgs

	fs := flag.NewFlagSet("add", flag.ExitOnError)
	root := fs.String("root", ".", "catalog repo root")
	system := fs.String("system", "", "proof-system family, e.g. longfellow")
	id := fs.String("id", "", "explicit id (derived from filename convention if omitted, longfellow only)")
	origin := fs.String("origin", "", "where these bytes came from (spec §2.8) — required; a commit-pinned URL (e.g. a GitHub blob link with a commit SHA in it) is a complete reference on its own, no separate ref/path fields needed")
	toolchain := fs.String("toolchain", "", "what actually built these bytes: compiler/tool name+version, build command, or CI job (spec §2.8.1) — leave unset if unknown, do not write a paragraph explaining why")
	license := fs.String("license", "", "SPDX id or short statement")
	openSource := fs.Bool("open-source", false, "affirmatively claim this is open source (spec §2.8.1) — default false, NOT inferred from --license")
	notes := fs.String("notes", "", "free text for humans (spec §2.4) — clients MUST NOT parse it")
	unpublished := fs.Bool("unpublished", false, "keep this entry out of the served manifest (spec §2.4.1) — default is published")
	generatedAt := fs.String("generated-at", "", "manifest generatedAt to write (RFC3339); defaults to now")
	var aliases, docTypes, params repeatedFlag
	fs.Var(&aliases, "alias", "additional resolvable id (repeatable)")
	fs.Var(&docTypes, "doc-type", "supported docType (repeatable)")
	fs.Var(&params, "param", "key=value, cross-checked against the filename when the key is filename-derived (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	explicitParams := map[string]string{}
	for _, p := range params {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return fmt.Errorf("--param must be key=value, got %q", p)
		}
		explicitParams[k] = v
	}

	result, err := publish.Add(*root, publish.AddOptions{
		InputFile:      file,
		System:         *system,
		ID:             *id,
		Aliases:        aliases,
		DocTypes:       docTypes,
		Origin:         *origin,
		Toolchain:      *toolchain,
		License:        *license,
		OpenSource:     *openSource,
		Notes:          *notes,
		Unpublished:    *unpublished,
		ExplicitParams: explicitParams,
	})
	if err != nil {
		return err
	}
	fmt.Printf("added %s (published=%t)\n  artifact: %s (%d bytes, %s)\n  entry:    %s\n",
		result.Entry.ID, result.Entry.Published, result.ArtifactPath, result.Entry.Artifact.Size, result.Entry.Artifact.Hash, result.EntryPath)

	genAt := *generatedAt
	if genAt == "" {
		genAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := publish.RegenerateManifest(*root, genAt); err != nil {
		return fmt.Errorf("added the entry but failed to regenerate manifest.json: %w", err)
	}
	fmt.Println("regenerated catalog/manifest.json")
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	root := fs.String("root", ".", "catalog repo root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := publish.Verify(*root)
	if err != nil {
		return err
	}
	fmt.Printf("checked %d entries, %d artifacts\n", report.EntriesChecked, report.ArtifactsChecked)
	for _, p := range report.Problems {
		fmt.Fprintln(os.Stderr, "FAIL:", p)
	}
	if !report.OK() {
		return fmt.Errorf("%d problem(s) found", len(report.Problems))
	}
	fmt.Println("OK")
	return nil
}

func runLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	root := fs.String("root", ".", "catalog repo root")
	stale := fs.Bool("stale", false, "only show active entries with no recorded interop verification (spec §5.7)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stale {
		rows, err := publish.StaleRows(*root)
		if err != nil {
			return err
		}
		for _, r := range rows {
			fmt.Printf("%-45s %-12s %-4s STALE (no verifiedBy recorded)\n", r.ID, r.System, r.SystemVersion)
		}
		return nil
	}
	rows, err := publish.Ls(*root)
	if err != nil {
		return err
	}
	fmt.Printf("%-45s %-12s %-4s %-9s %-10s %10s %s\n", "ID", "SYSTEM", "VER", "PUBLISHED", "STATUS", "BYTES", "VERIFIED")
	for _, r := range rows {
		fmt.Printf("%-45s %-12s %-4s %-9t %-10s %10d %d\n", r.ID, r.System, r.SystemVersion, r.Published, r.Status, r.Size, r.VerifiedCount)
	}
	return nil
}

// reasonRequired mirrors spec §5.3: deprecate/revoke are client-visible,
// consequential actions and must be justified; publish/unpublish are
// neither (an unpublished entry is simply absent either way), so a reason
// is accepted for the audit trail but not required.
func reasonRequired(action string) bool {
	return action == "deprecate" || action == "revoke"
}

func runLifecycle(args []string, action string) error {
	id, flagArgs, err := splitLeadingPositional(args, nil) // deprecate/revoke/publish/unpublish have no boolean flags
	if err != nil {
		reasonUsage := "--reason <text>"
		if !reasonRequired(action) {
			reasonUsage = "[--reason <text>]"
		}
		return fmt.Errorf("%w (usage: circuitctl %s <id> %s [options])", err, action, reasonUsage)
	}
	args = flagArgs

	fs := flag.NewFlagSet(action, flag.ExitOnError)
	root := fs.String("root", ".", "catalog repo root")
	reasonHelp := "required — this is a client-visible action"
	if !reasonRequired(action) {
		reasonHelp = "optional — recorded in notes for the audit trail"
	}
	reason := fs.String("reason", "", reasonHelp)
	generatedAt := fs.String("generated-at", "", "manifest generatedAt to write (RFC3339); defaults to now")
	if err := fs.Parse(args); err != nil {
		return err
	}

	switch action {
	case "deprecate":
		err = publish.Deprecate(*root, id, *reason)
	case "revoke":
		err = publish.Revoke(*root, id, *reason)
	case "publish":
		err = publish.Publish(*root, id, *reason)
	case "unpublish":
		err = publish.Unpublish(*root, id, *reason)
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", action, id)

	genAt := *generatedAt
	if genAt == "" {
		genAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := publish.RegenerateManifest(*root, genAt); err != nil {
		return fmt.Errorf("%s succeeded but failed to regenerate manifest.json: %w", action, err)
	}
	fmt.Println("regenerated catalog/manifest.json")
	return nil
}
