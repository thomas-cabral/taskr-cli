// cli/snapshot_cmd_test.go
package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// snapshotEnv is a full set of tree-state variables, as the taskr skill
// tells a caller to export them. The CLI never runs git: every value here
// is the output of a git command the CALLER ran.
func snapshotEnv(api string) map[string]string {
	return map[string]string{
		"TASKR_API":        api,
		"TASKR_KEY":        "x",
		"TASKR_REMOTE":     "git@github.com:acme/spillway.git",
		"TASKR_ROOT":       "/home/tc/spillway/.git/worktrees/billing",
		"TASKR_HEAD":       "abc123",
		"TASKR_BRANCH":     "fix/bug-sweep",
		"TASKR_MERGE_BASE": "def456",
		"TASKR_DIRTY":      "cli/cli.go\ncli/client.go",
	}
}

// TestNewSendsTheGitSnapshot is TSK-110: the renderer has always printed a
// "Tree state:" block, and nothing ever sent one, so every issue the CLI
// wrote read "no git snapshot has been recorded for this issue".
func TestNewSendsTheGitSnapshot(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"id-1","ref":"TSK-1"}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	if code := Run([]string{"new", "list perf"}, &out, &errb, envAt(snapshotEnv(srv.URL))); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, want := range []string{
		`"repo":"git@github.com:acme/spillway.git"`,
		`"branch":"fix/bug-sweep"`,
		`"head_sha":"abc123"`,
		`"worktree":"/home/tc/spillway/.git/worktrees/billing"`,
		`"merge_base":"def456"`,
		`"dirty_files":["cli/cli.go","cli/client.go"]`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body is missing %s\ngot %s", want, gotBody)
		}
	}
}

// TestNewOmitsTheSnapshotWithoutAHead pins the rule stepSnapshot already
// follows: no head, no snapshot. An empty one on the wire would turn the
// honest "none recorded" line into a block of blanks.
//
// "Without a head" now means the caller exported none AND there is no
// checkout to read one out of, which is why this runs from a directory that
// is not a repo. Anywhere inside one, discoverRepo answers and a snapshot
// is exactly what should be sent.
func TestNewOmitsTheSnapshotWithoutAHead(t *testing.T) {
	t.Chdir(t.TempDir())
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"id-1","ref":"TSK-1"}`)
	}))
	defer srv.Close()

	env := snapshotEnv(srv.URL)
	delete(env, "TASKR_HEAD")

	var out, errb bytes.Buffer
	if code := Run([]string{"new", "list perf"}, &out, &errb, envAt(env)); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if strings.Contains(gotBody, "git_snapshot") {
		t.Fatalf("want no git_snapshot at all with TASKR_HEAD unset, got %s", gotBody)
	}
}

// TestNewSendsADetachedHeadSnapshot covers the case the gate deliberately
// lets through: a detached HEAD has no branch, and the head is still worth
// recording. The branch goes over as empty and the renderer says
// "(detached)" — omitted openly rather than silently.
//
// Run from outside a checkout: with TASKR_BRANCH deleted, a real repo
// underfoot would supply its own branch and the case under test would never
// arise.
func TestNewSendsADetachedHeadSnapshot(t *testing.T) {
	t.Chdir(t.TempDir())
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"id-1","ref":"TSK-1"}`)
	}))
	defer srv.Close()

	env := snapshotEnv(srv.URL)
	delete(env, "TASKR_BRANCH")

	var out, errb bytes.Buffer
	if code := Run([]string{"new", "list perf"}, &out, &errb, envAt(env)); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"head_sha":"abc123"`) {
		t.Fatalf("want the head recorded with no branch, got %s", gotBody)
	}
	if strings.Contains(gotBody, `"branch":`) {
		t.Fatalf("want branch omitted rather than sent empty, got %s", gotBody)
	}
}

// TestOffloadSendsTheGitSnapshot — an offload is filed from wherever the
// caller is standing, so the tree it was noticed in is exactly what the
// next reader needs.
func TestOffloadSendsTheGitSnapshot(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/context":
			fmt.Fprint(w, `{"machine":"laptop","active_session":{"id":"sess-1","machine":"laptop"}}`)
		case "/api/offload":
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			fmt.Fprint(w, `{"id":"id-2","ref":"TSK-2"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	args := []string{"offload", "auth drops the cookie", "-m", "internal/api/auth.go:104"}
	if code := Run(args, &out, &errb, envAt(snapshotEnv(srv.URL))); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"branch":"fix/bug-sweep"`) || !strings.Contains(gotBody, `"head_sha":"abc123"`) {
		t.Fatalf("offload body carries no snapshot: %s", gotBody)
	}
}

// TestParkSendsTheGitSnapshot — a park is the handoff, so the branch the
// work was left on is the single most useful fact it can carry.
func TestParkSendsTheGitSnapshot(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/context":
			fmt.Fprint(w, `{"machine":"laptop","active_session":{"id":"sess-1","machine":"laptop"},"active_issue":{"ref":"TSK-1"}}`)
		case "/api/work/park":
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	args := []string{"park", "-m", "add the empty-header case to auth_test.go", "-r", "done_for_now"}
	if code := Run(args, &out, &errb, envAt(snapshotEnv(srv.URL))); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"branch":"fix/bug-sweep"`) || !strings.Contains(gotBody, `"head_sha":"abc123"`) {
		t.Fatalf("park body carries no snapshot: %s", gotBody)
	}
}

// TestUsageNamesTheSnapshotVariables — a variable nothing documents is a
// variable nobody exports, and the snapshot is only as good as the
// environment handed to it (TSK-110's done-when).
func TestUsageNamesTheSnapshotVariables(t *testing.T) {
	var out, errb bytes.Buffer
	Run([]string{"--help"}, &out, &errb, envAt(map[string]string{}))
	text := out.String() + errb.String()
	for _, want := range []string{"TASKR_BRANCH", "TASKR_DIRTY", "TASKR_MERGE_BASE"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage text does not name %s:\n%s", want, text)
		}
	}
}
