// cli/conventions_cmd_test.go
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

// projectsWithConventions is what GET /api/projects answers in the tests
// below: one project carrying all three conventions, one carrying none.
const projectsWithConventions = `[
	{"id":"p-1","slug":"taskr","name":"taskr","key":"TSK","created_at":"2026-08-24T00:00:00Z",
	 "conventions":{"branch_format":"tc/{key}-{n}--{slug}","commit_style":"conventional","pr_target":"master"},
	 "repos":[{"id":"r-1","remote_url":"git@github.com:acme/taskr.git","host":"github.com","owner":"acme","name":"taskr"}]},
	{"id":"p-2","slug":"spillway","name":"Spillway","key":"SPL","created_at":"2026-08-24T00:00:00Z",
	 "conventions":{}}
]`

// TestProjectLsRendersConventions is TSK-111: branch_format, commit_style
// and pr_target are columns, are settable over HTTP, are served on the read
// model and are decoded by this client — and nothing ever printed them, so
// every agent guessed the base branch instead of reading the answer
// somebody had already recorded.
func TestProjectLsRendersConventions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, projectsWithConventions)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"project", "ls"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, want := range []string{"tc/{key}-{n}--{slug}", "conventional", "master"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("project ls does not print %q:\n%s", want, out.String())
		}
	}
}

// TestProjectLsOmitsBlankConventions pins the other half: a project with no
// conventions prints no convention lines at all, rather than a column of
// empty labels.
func TestProjectLsOmitsBlankConventions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, projectsWithConventions)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"project", "ls"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	// Everything after the second project's header line belongs to it.
	_, spillway, ok := strings.Cut(out.String(), "spillway")
	if !ok {
		t.Fatalf("spillway is missing from the listing:\n%s", out.String())
	}
	for _, never := range []string{"branch", "commit", "pr target"} {
		if strings.Contains(spillway, never) {
			t.Errorf("a project with no conventions printed %q:\n%s", never, spillway)
		}
	}
}

// TestContextSurfacesConventions — the context read is where an agent
// orients, and the base branch and branch-name shape are exactly what it
// needs before opening a branch or a PR.
func TestContextSurfacesConventions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"machine":"laptop","open_issues":3,
			"project":{"id":"p-1","slug":"taskr","name":"taskr","key":"TSK","created_at":"2026-08-24T00:00:00Z",
			"conventions":{"branch_format":"tc/{key}-{n}--{slug}","pr_target":"master"}}}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, want := range []string{"tc/{key}-{n}--{slug}", "master"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("context does not surface %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "commit") {
		t.Errorf("context printed an unset convention:\n%s", out.String())
	}
}

// TestProjectInitSendsConventions is the second half of TSK-111's
// done-when: the values have to be reachable without hand-written curl.
// SetupProject is an upsert that refreshes conventions when they are
// non-empty, so init both creates a project with them and sets them on one
// that already exists.
func TestProjectInitSendsConventions(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"project_id":"p-1","key":"TSK","claude_md_snippet":"..."}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{
		"project", "init", "taskr", "--key", "TSK",
		"--branch-format", "tc/{key}-{n}--{slug}",
		"--commit-style", "conventional",
		"--pr-target", "master",
	}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, want := range []string{
		`"branch_format":"tc/{key}-{n}--{slug}"`,
		`"commit_style":"conventional"`,
		`"pr_target":"master"`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("init body is missing %s\ngot %s", want, gotBody)
		}
	}
}

// TestProjectInitOmitsUnsetConventions matters because the server's upsert
// only overwrites a convention when the incoming value is non-empty. An
// init that sends an empty conventions block is harmless today, but sending
// nothing is what makes that harmlessness independent of the server's rule.
func TestProjectInitOmitsUnsetConventions(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"project_id":"p-1","key":"TSK","claude_md_snippet":"..."}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"project", "init", "taskr", "--key", "TSK"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if strings.Contains(gotBody, "conventions") {
		t.Fatalf("want no conventions block when none were named, got %s", gotBody)
	}
}
