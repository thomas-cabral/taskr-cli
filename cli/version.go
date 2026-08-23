// cli/version.go
package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// buildStamp is what this binary already knows about its own provenance.
//
// Nothing here is stamped by the Makefile: the go tool records vcs.revision,
// vcs.time and vcs.modified into every main package it builds out of a git
// work tree, so an ad-hoc `go build` and `make build` are stamped alike and
// there is no build incantation to remember or forget. A binary built with
// -buildvcs=false, or `go test`'s in-memory binary, simply has no revision —
// which reads as "cannot tell", never as "current".
type buildStamp struct {
	Module    string
	Revision  string
	Time      string
	Dirty     bool
	GoVersion string
}

// readBuildStamp pulls the stamp out of the running binary. It is a var so
// tests can say what the stamp is: a `go test` binary carries no VCS stamp
// at all, which makes the stale and dirty paths unreachable from inside the
// suite otherwise.
var readBuildStamp = func() buildStamp {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return buildStamp{}
	}
	s := buildStamp{Module: info.Main.Path, GoVersion: info.GoVersion}
	for _, kv := range info.Settings {
		switch kv.Key {
		case "vcs.revision":
			s.Revision = kv.Value
		case "vcs.time":
			s.Time = kv.Value
		case "vcs.modified":
			s.Dirty = kv.Value == "true"
		}
	}
	return s
}

// rebuildHint is the fast path back to a current binary. `make build` works
// too, but it runs `make web` first — a pnpm install and a SvelteKit build —
// which is a long way round when only the CLI changed.
const rebuildHint = "go build -o bin/taskr ./cmd/taskr-cli"

// stalenessWarning says, in one line, whether the taskr that is running is
// the taskr in the checkout the caller is standing in.
//
// It answers only when it can. The comparison is meaningful exactly when the
// caller is standing in taskr's own source repo, which is what rootModule
// establishes: the module path declared by the go.mod at TASKR_ROOT. From
// any other repo, TASKR_HEAD is that repo's head and has nothing to say
// about this binary — comparing them would fire the warning in every project
// on the machine but one, and a warning that is usually wrong is a warning
// nobody reads.
//
// Everything else that cannot be established reads the same way: no
// TASKR_HEAD, no VCS stamp, no go.mod — silence, not an accusation.
func stalenessWarning(s buildStamp, headSHA, rootModule string) string {
	if s.Module == "" || s.Revision == "" || rootModule == "" || rootModule != s.Module {
		return ""
	}
	if s.Dirty {
		return fmt.Sprintf(
			"this taskr was built from a modified tree, so %s does not identify the code it is running.\n"+
				"      Rebuild to be sure of what you are testing: %s",
			short(s.Revision), rebuildHint)
	}
	headSHA = strings.TrimSpace(headSHA)
	if headSHA == "" || sameCommit(s.Revision, headSHA) {
		return ""
	}
	// Both revisions, always. "taskr is stale" alone sends the reader back
	// to exactly the guessing this warning exists to end.
	return fmt.Sprintf(
		"this taskr was built from %s but the checkout is at %s — you are running old code.\n"+
			"      Rebuild before trusting what it says: %s",
		short(s.Revision), short(headSHA), rebuildHint)
}

// sameCommit compares two revisions that may be recorded at different
// lengths — `git rev-parse HEAD` gives forty characters, `--short` gives
// seven — so a prefix match is the same commit rather than a different one.
func sameCommit(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a)
}

// short renders a revision the length a human reads it at.
func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// modulePathAt reads the module path declared by the go.mod at root, or ""
// when there is no go.mod to read — the repo is not a Go module, the path is
// unset, or it simply is not readable. Every one of those means "cannot
// tell", which is what stalenessWarning wants to hear.
func modulePathAt(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if path, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

// stalenessFor is the environment-facing wrapper: it gathers the two facts
// stalenessWarning needs from where a caller has already put them.
func stalenessFor(getenv func(string) string) string {
	return stalenessWarning(readBuildStamp(),
		getenv("TASKR_HEAD"), modulePathAt(getenv("TASKR_ROOT")))
}

// versionView is the machine-readable answer to "what am I running, and does
// it match what I am looking at". stale is the judgement rather than a fact
// to re-derive: a caller that has to compare the two revisions itself is
// back where it started.
type versionView struct {
	Module           string `json:"module"`
	Revision         string `json:"revision,omitempty"`
	Time             string `json:"time,omitempty"`
	Dirty            bool   `json:"dirty"`
	GoVersion        string `json:"go_version,omitempty"`
	CheckoutRevision string `json:"checkout_revision,omitempty"`
	Stale            bool   `json:"stale"`
	Warning          string `json:"warning,omitempty"`
}

// cmdVersion answers the one question this whole file exists for, and does
// it without touching the network. A binary suspected of being stale is
// exactly the one that may be pointed at the wrong host or hold no
// credential, so an answer that needed the API would be unavailable in the
// case it is wanted for.
func cmdVersion(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	s := readBuildStamp()
	head := strings.TrimSpace(getenv("TASKR_HEAD"))
	warning := stalenessWarning(s, head, modulePathAt(getenv("TASKR_ROOT")))

	if *jsonOut {
		return printJSON(stdout, versionView{
			Module: s.Module, Revision: s.Revision, Time: s.Time, Dirty: s.Dirty,
			GoVersion: s.GoVersion, CheckoutRevision: head,
			Stale: warning != "", Warning: warning,
		})
	}

	fmt.Fprintf(stdout, "taskr %s\n", s.Module)
	if s.Revision == "" {
		// Not an error, but not nothing either: an unstamped binary can
		// never be compared to a checkout, and saying so beats printing a
		// blank line the reader has to interpret.
		fmt.Fprintln(stdout, "  revision  unstamped (built with -buildvcs=false, or not from a git tree)")
	} else {
		dirty := ""
		if s.Dirty {
			dirty = " (modified tree)"
		}
		fmt.Fprintf(stdout, "  revision  %s%s\n", short(s.Revision), dirty)
	}
	if s.Time != "" {
		fmt.Fprintf(stdout, "  built     %s\n", s.Time)
	}
	if s.GoVersion != "" {
		fmt.Fprintf(stdout, "  go        %s\n", s.GoVersion)
	}
	if head != "" {
		fmt.Fprintf(stdout, "  checkout  %s\n", short(head))
	}
	if warning != "" {
		fmt.Fprintln(stderr, "taskr:", warning)
	}
	return nil
}
