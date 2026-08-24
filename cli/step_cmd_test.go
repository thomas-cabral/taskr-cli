// cli/step_cmd_test.go
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

// stepsJSON is the plan `ListSteps` decodes for TSK-1 across the tests
// below: two ordinary steps, at positions 1 and 2, to resolve a position
// selector against.
const stepsJSON = `[
	{"id":"s-1","position":1,"title":"read auth.go","status":"done"},
	{"id":"s-2","position":2,"title":"add cookie fallback","status":"in_progress"}
]`

// TestStepDoneResolvesPosition exercises the settled position-or-id rule:
// a small integer selector is the position `step ls` printed, and
// resolving it costs one extra read (ListSteps) to find the step id that
// currently sits there — the write then addresses that id, not the
// position.
func TestStepDoneResolvesPosition(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues/TSK-1/steps":
			fmt.Fprint(w, stepsJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/api/steps/s-2/done":
			gotMethod, gotPath = r.Method, r.URL.Path
			fmt.Fprint(w, `{"status":"done"}`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"step", "done", "TSK-1", "2"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/steps/s-2/done" {
		t.Fatalf("want POST /api/steps/s-2/done, got %s %s", gotMethod, gotPath)
	}
}

// TestStepDoneWithIDSkipsListSteps exercises the other half of the rule: a
// selector that does not parse as a small positive integer is already a
// step id, and resolving it costs no read at all — ListSteps must never be
// called.
func TestStepDoneWithIDSkipsListSteps(t *testing.T) {
	listCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/issues/TSK-1/steps":
			listCalled = true
			fmt.Fprint(w, stepsJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/api/steps/step-xyz/done":
			fmt.Fprint(w, `{"status":"done"}`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"step", "done", "TSK-1", "step-xyz"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if listCalled {
		t.Fatal("want no ListSteps call when the selector is already a step id")
	}
}

// TestStepPositionOutOfRange exercises the range check: a position with no
// matching row is an error naming the valid range, and the write is never
// reached.
func TestStepPositionOutOfRange(t *testing.T) {
	writeCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues/TSK-1/steps":
			fmt.Fprint(w, stepsJSON)
		default:
			writeCalled = true
			t.Fatalf("unexpected %s %s — the write must not be reached", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	code := Run([]string{"step", "done", "TSK-1", "5"}, &out, &errb, env)
	if code == 0 {
		t.Fatalf("want non-zero exit for an out-of-range position, got 0")
	}
	if writeCalled {
		t.Fatal("want no write for an out-of-range position")
	}
	if !strings.Contains(errb.String(), "1-2") {
		t.Fatalf("want the valid range named in the error, got: %s", errb.String())
	}
}

// TestStepAddSendsTitlesArray exercises `step add` with several titles:
// they land on the wire as one titles array in one request, not one call
// per title.
func TestStepAddSendsTitlesArray(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `[{"id":"s-1","issue":{"id":"i-1","ref":"TSK-1"}},{"id":"s-2","issue":{"id":"i-1","ref":"TSK-1"}}]`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"step", "add", "TSK-1", "read auth.go", "add cookie fallback"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"titles":["read auth.go","add cookie fallback"]`) {
		t.Fatalf("want both titles in one titles array, got %s", gotBody)
	}
}

// TestStepMvFront exercises `step mv --front`: it sends an empty after —
// the wire spelling for "move to the front of the plan".
func TestStepMvFront(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api/steps/step-1/position" {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"step", "mv", "TSK-1", "step-1", "--front"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"after":""`) {
		t.Fatalf("want an empty after in the request body, got %s", gotBody)
	}
}

// TestStepPromoteDefaults exercises `step promote`'s defaults: became
// defaults to "issue", and block is omitted from the wire entirely so the
// server's own default (true, for a child issue) applies.
func TestStepPromoteDefaults(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"step_id":"step-1","became":"issue","target_id":"i-2","target_ref":"TSK-2","blocked":true}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"step", "promote", "TSK-1", "step-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"became":"issue"`) {
		t.Fatalf("want became:issue in the request body, got %s", gotBody)
	}
	if strings.Contains(gotBody, `"block"`) {
		t.Fatalf("want block omitted so the server default applies, got %s", gotBody)
	}
}

// TestStepPromoteNoBlock exercises --no-block: it sends block:false rather
// than omitting the field.
func TestStepPromoteNoBlock(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"step_id":"step-1","became":"issue","target_id":"i-2","target_ref":"TSK-2","blocked":false}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"step", "promote", "TSK-1", "step-1", "--no-block"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"block":false`) {
		t.Fatalf("want block:false in the request body, got %s", gotBody)
	}
}

// TestStepStartWithHeadSHA exercises the mark snapshot rule: TASKR_HEAD,
// when set, becomes the mark's git_snapshot carrying head_sha and nothing
// else — this client never runs git, so branch and dirty-count are never
// on the wire at all.
func TestStepStartWithHeadSHA(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"in_progress"}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x", "TASKR_HEAD": "abc123"})
	if code := Run([]string{"step", "start", "TSK-1", "step-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"git_snapshot":{"head_sha":"abc123"}`) {
		t.Fatalf("want git_snapshot with only head_sha, got %s", gotBody)
	}
}

// TestStepStartWithoutHeadSHA exercises the other half: with no
// TASKR_HEAD, the snapshot is omitted from the wire entirely, not sent
// empty. Run from outside any checkout, since a repo underfoot is a head
// the CLI can read for itself.
func TestStepStartWithoutHeadSHA(t *testing.T) {
	t.Chdir(t.TempDir())
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"in_progress"}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"step", "start", "TSK-1", "step-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if strings.Contains(gotBody, "git_snapshot") {
		t.Fatalf("want no git_snapshot at all when TASKR_HEAD is unset, got %s", gotBody)
	}
}

// TestStepLsRendersPlan exercises `step ls`'s human output end to end: it
// reads the issue, not the steps endpoint, and the row for the in-progress
// step names the SHA its latest mark recorded, so a resuming reader can
// tell whether their tree matches the plan without a second command.
func TestStepLsRendersPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/issues/TSK-1" {
			t.Fatalf("unexpected %s %s — step ls should GET the issue", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"i-1","ref":"TSK-1","title":"add cookie fallback",
			"steps":[
				{"id":"s-1","position":1,"title":"read auth.go","status":"done"},
				{"id":"s-2","position":2,"title":"add cookie fallback","status":"in_progress",
				 "marks":[{"kind":"start","head_sha":"abc123","actor":"agent","recorded_at":"2026-08-23T00:00:00Z"}]},
				{"id":"s-3","position":3,"title":"add integration test","status":"pending"}
			],
			"step_progress":{"done":1,"total":3}}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"step", "ls", "TSK-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"TSK-1", "add cookie fallback", "1/3 done", "read auth.go", "abc123", "add integration test"} {
		if !strings.Contains(got, want) {
			t.Fatalf("step ls output missing %q:\n%s", want, got)
		}
	}
}

// TestStepLsUsesServerProgress exercises the reason `step ls` reads the
// issue instead of recomputing a fraction from the rows: the server's
// step_progress is what prints, even when it disagrees with what a naive
// count of the rows below it would give — proving the number on screen
// came off the wire, not out of a second copy of the counting rule here.
func TestStepLsUsesServerProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A naive count of this one "done" row would read 1/1. step_progress
		// deliberately says otherwise.
		fmt.Fprint(w, `{"id":"i-1","ref":"TSK-1","title":"ship it",
			"steps":[{"id":"s-1","position":1,"title":"only step","status":"done"}],
			"step_progress":{"done":9,"total":10}}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"step", "ls", "TSK-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "9/10 done") {
		t.Fatalf("want the server's step_progress (9/10) printed, got:\n%s", got)
	}
	if strings.Contains(got, "1/1 done") {
		t.Fatalf("want no locally recomputed fraction, got:\n%s", got)
	}
}

// TestStepLsNoSteps exercises an issue with no plan yet: the server omits
// both steps and step_progress (StepProgress is nil for an empty plan),
// and `step ls` renders the empty-plan line rather than a bogus fraction
// or a panic on a nil StepProgress.
func TestStepLsNoSteps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"i-1","ref":"TSK-1","title":"ship it"}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"step", "ls", "TSK-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	if strings.Contains(got, "done") {
		t.Fatalf("want no progress fraction for a plan-less issue, got:\n%s", got)
	}
	if !strings.Contains(got, "no steps yet") {
		t.Fatalf("want the empty-plan line, got:\n%s", got)
	}
}

// TestStepEditExplicitEmptyTitle exercises the pointer wiring at the heart
// of `step edit`: an explicitly empty --title is non-nil and must reach
// the wire as "title":"" so the server's own refusal fires — coalescing
// it with "flag not passed" would swallow the refusal here instead.
func TestStepEditExplicitEmptyTitle(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"step", "edit", "TSK-1", "step-1", "--title", ""}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"title":""`) {
		t.Fatalf("want an explicit empty title in the request body, got %s", gotBody)
	}
}

// TestStepEditOnlyTitle exercises the other half of the pointer wiring:
// --title alone must not put an unset body on the wire.
func TestStepEditOnlyTitle(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"step", "edit", "TSK-1", "step-1", "--title", "new title"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"title":"new title"`) {
		t.Fatalf("want the new title in the request body, got %s", gotBody)
	}
	if strings.Contains(gotBody, `"body"`) {
		t.Fatalf("want no body field when --body was not passed, got %s", gotBody)
	}
}

// TestStepEditOnlyBody is the mirror of TestStepEditOnlyTitle: --body
// alone must not put an unset title on the wire.
func TestStepEditOnlyBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"step", "edit", "TSK-1", "step-1", "--body", "new body text"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"body":"new body text"`) {
		t.Fatalf("want the new body in the request body, got %s", gotBody)
	}
	if strings.Contains(gotBody, `"title"`) {
		t.Fatalf("want no title field when --title was not passed, got %s", gotBody)
	}
}

// TestStepEditNoFlagsSkipsListSteps exercises the ordering fix: a purely
// local usage error (neither --title nor --body given) is caught before
// resolveStepID runs, so a position selector never costs a ListSteps round
// trip that was always going to be wasted on a command that fails anyway.
func TestStepEditNoFlagsSkipsListSteps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected %s %s — no request should be made", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	code := Run([]string{"step", "edit", "TSK-1", "2"}, &out, &errb, env)
	if code == 0 {
		t.Fatalf("want non-zero exit when neither --title nor --body is given, got 0")
	}
}

// TestStepDropWireShape exercises `step drop`'s wire shape end to end:
// POST /api/steps/<id>/drop, the reason in the request body, and the
// response decoded as the plan ([]StepView) rather than anything wrapped.
func TestStepDropWireShape(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"step-1","position":1,"title":"read auth.go","status":"dropped","drop_reason":"superseded"}]`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"step", "drop", "TSK-1", "step-1", "-m", "superseded"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/steps/step-1/drop" {
		t.Fatalf("want POST /api/steps/step-1/drop, got %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"reason":"superseded"`) {
		t.Fatalf("want the reason in the request body, got %s", gotBody)
	}
	// Decoded correctly as []StepView, not swallowed or misshaped: the
	// dropped row's own status and drop_reason come back out in the
	// rendered plan.
	got := out.String()
	if !strings.Contains(got, "dropped: superseded") {
		t.Fatalf("want the dropped step rendered with its reason, got:\n%s", got)
	}
}

// TestStepSessionID exercises the one field every step write is supposed
// to carry: session_id lands in the request body. Picked on step start,
// but nothing here is start-specific — the point is that a future change
// dropping session_id from any one verb would be caught somewhere.
func TestStepSessionID(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"in_progress"}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x", "TASKR_SESSION": "sess-42"})
	if code := Run([]string{"step", "start", "TSK-1", "step-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"session_id":"sess-42"`) {
		t.Fatalf("want session_id in the request body, got %s", gotBody)
	}
}
