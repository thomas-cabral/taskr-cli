// cli/version_test.go
package cli

import (
	"os"
	"strings"
	"testing"
)

const (
	built = "9785768f9bf5bceafc48b262cc8c4f44234d306c"
	head  = "dc72cac8c23bfcb2ebb99d209299d4c950622051"
	mod   = "github.com/thomas-cabral/taskr"
)

func TestStalenessWarningSilentWhenBinaryMatchesCheckout(t *testing.T) {
	s := buildStamp{Module: mod, Revision: built}
	if w := stalenessWarning(s, built, mod); w != "" {
		t.Fatalf("want no warning when the binary is the checkout, got %q", w)
	}
}

func TestStalenessWarningNamesBothRevisions(t *testing.T) {
	s := buildStamp{Module: mod, Revision: built}
	w := stalenessWarning(s, head, mod)
	if w == "" {
		t.Fatal("want a warning when the binary lags the checkout, got none")
	}
	// Both halves of the fact have to be in the message: which code is
	// running and which code the caller is looking at. A warning that says
	// only "stale" sends the reader back to the same guessing the whole
	// issue is about.
	if !strings.Contains(w, built[:12]) || !strings.Contains(w, head[:12]) {
		t.Fatalf("want both revisions named, got %q", w)
	}
	// And the way out. `make build` also runs pnpm, so the fast path is the
	// one worth printing.
	if !strings.Contains(w, "go build -o bin/taskr ./cmd/taskr") {
		t.Fatalf("want the rebuild command, got %q", w)
	}
}

// Standing in some other repo, TASKR_HEAD is that repo's head and has
// nothing to do with taskr's. Comparing them would fire the warning in
// every project on the machine except one, which trains the reader to
// ignore it.
func TestStalenessWarningSilentOutsideTheTaskrCheckout(t *testing.T) {
	s := buildStamp{Module: mod, Revision: built}
	if w := stalenessWarning(s, head, "github.com/acme/widgets"); w != "" {
		t.Fatalf("want no warning from an unrelated repo, got %q", w)
	}
	if w := stalenessWarning(s, head, ""); w != "" {
		t.Fatalf("want no warning when the standing repo is unknown, got %q", w)
	}
}

// No TASKR_HEAD means the caller has not told us what the checkout is at.
// Absence of evidence is not staleness.
func TestStalenessWarningSilentWithoutAHead(t *testing.T) {
	s := buildStamp{Module: mod, Revision: built}
	if w := stalenessWarning(s, "", mod); w != "" {
		t.Fatalf("want no warning without TASKR_HEAD, got %q", w)
	}
}

// A binary with no VCS stamp (go test, or a build with -buildvcs=false)
// cannot be compared. Say nothing rather than accusing it.
func TestStalenessWarningSilentWithoutAStamp(t *testing.T) {
	s := buildStamp{Module: mod}
	if w := stalenessWarning(s, head, mod); w != "" {
		t.Fatalf("want no warning from an unstamped binary, got %q", w)
	}
}

// git rev-parse --short HEAD is a plausible thing to have exported. A
// prefix of the built revision is the same commit, not a different one.
func TestStalenessWarningAcceptsAShortHead(t *testing.T) {
	s := buildStamp{Module: mod, Revision: built}
	if w := stalenessWarning(s, built[:7], mod); w != "" {
		t.Fatalf("want no warning for a short-form head of the same commit, got %q", w)
	}
}

// vcs.modified means the revision does not identify the code that was
// compiled, so a matching SHA proves nothing.
func TestStalenessWarningReportsADirtyBuild(t *testing.T) {
	s := buildStamp{Module: mod, Revision: built, Dirty: true}
	w := stalenessWarning(s, built, mod)
	if w == "" {
		t.Fatal("want a warning for a binary built from a modified tree, got none")
	}
	if !strings.Contains(w, "modified") {
		t.Fatalf("want the message to say the tree was modified, got %q", w)
	}
}

func TestModulePathAtReadsGoMod(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/go.mod", "// a comment\nmodule github.com/acme/widgets\n\ngo 1.26\n"); err != nil {
		t.Fatal(err)
	}
	if got := modulePathAt(dir); got != "github.com/acme/widgets" {
		t.Fatalf("modulePathAt = %q, want github.com/acme/widgets", got)
	}
}

// Not every repo is a Go module, and TASKR_ROOT may be unset entirely.
// Both read back as "I cannot tell", which stalenessWarning treats as
// "say nothing".
func TestModulePathAtIsEmptyWithoutAGoMod(t *testing.T) {
	if got := modulePathAt(t.TempDir()); got != "" {
		t.Fatalf("modulePathAt on a non-module dir = %q, want empty", got)
	}
	if got := modulePathAt(""); got != "" {
		t.Fatalf("modulePathAt(\"\") = %q, want empty", got)
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o644) }
