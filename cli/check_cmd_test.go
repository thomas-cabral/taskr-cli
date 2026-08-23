// cli/check_cmd_test.go
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

func TestParseMeasure(t *testing.T) {
	m, err := parseMeasure("list.p50=0.057s", "c50")
	if err != nil {
		t.Fatal(err)
	}
	if m.Metric != "list.p50" || m.Value != 0.057 || m.Unit != "s" || m.Conditions != "c50" {
		t.Fatalf("m = %+v", m)
	}
	m, err = parseMeasure("count=42", "")
	if err != nil || m.Unit != "" || m.Value != 42 {
		t.Fatalf("unitless: %+v, %v", m, err)
	}
	if _, err := parseMeasure("nometric", ""); err == nil {
		t.Fatal("missing = accepted")
	}
	if _, err := parseMeasure("m=notanumber", ""); err == nil {
		t.Fatal("non-numeric accepted")
	}
	if _, err := parseMeasure("m=1e309", ""); err == nil {
		t.Fatal("out-of-range value accepted")
	}
	if _, err := parseMeasure("m=+Inf", ""); err == nil {
		t.Fatal("+Inf accepted")
	}
	if _, err := parseMeasure("m=NaN", ""); err == nil {
		t.Fatal("NaN accepted")
	}
	m, err = parseMeasure("m=-0.5s", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Value != -0.5 || m.Unit != "s" {
		t.Fatalf("negative with unit: %+v", m)
	}
}

// TestCheckAdd exercises `check add` end to end: --human maps to
// runner=human on the wire, and the printed line names the check id so a
// caller can immediately follow up with `check run`.
func TestCheckAdd(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c-1","issue":{"id":"i-9","ref":"TSK-9"}}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"check", "add", "TSK-9", "-t", "lists fast", "-m", "hey -z 30s", "--expect", "> 100 r/s", "--human"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/issues/TSK-9/checks" {
		t.Fatalf("want POST /api/issues/TSK-9/checks, got %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"runner":"human"`) {
		t.Fatalf("want runner=human in the request body, got %s", gotBody)
	}
	if !strings.Contains(out.String(), "c-1") {
		t.Fatalf("want the check id printed, got:\n%s", out.String())
	}
}

// TestCheckLs exercises `check ls` end to end: the table it renders names
// each check's title, runner and status.
func TestCheckLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"c-1","title":"lists fast","runner":"agent","status":"open","created_at":"2026-08-01T00:00:00Z"}]`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"check", "ls", "TSK-9"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"lists fast", "agent", "open"} {
		if !strings.Contains(got, want) {
			t.Fatalf("check ls output missing %q:\n%s", want, got)
		}
	}
}

// TestCloseWithPendingChecks exercises the 409 a close over pending checks
// answers with: the refusal names each pending check's title and the exact
// --despite-checks spelling so a caller can act on it without reading docs,
// and the command exits non-zero.
func TestCloseWithPendingChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":"issue has pending checks","pending_checks":[{"id":"c-1","title":"lists fast","runner":"human"}]}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	code := Run([]string{"close", "TSK-9"}, &out, &errb, env)
	if code == 0 {
		t.Fatalf("want non-zero exit on a 409, got 0")
	}
	combined := out.String() + errb.String()
	if !strings.Contains(combined, "lists fast") {
		t.Fatalf("want the pending check's title in the output, got:\n%s", combined)
	}
	if !strings.Contains(combined, "--despite-checks") {
		t.Fatalf("want the --despite-checks spelling in the output, got:\n%s", combined)
	}
}

// TestCloseDespiteChecks exercises `close --despite-checks`: the flag lands
// on the wire as despite_checks:true.
func TestCloseDespiteChecks(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// close, on success, also checks whether the caller's own session
		// is still live on the issue (liveSessionOn) — that GET must not
		// clobber the PATCH body this test is asserting on.
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			fmt.Fprint(w, `{"id":"i-9","ref":"TSK-9"}`)
			return
		}
		fmt.Fprint(w, `{"machine":"m","open_issues":0}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"close", "TSK-9", "--despite-checks"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"despite_checks":true`) {
		t.Fatalf("want despite_checks:true in the request body, got %s", gotBody)
	}
}

// TestShowRendersChecks exercises `show` when the issue view carries
// checks: the checks table renders alongside the rest of the issue.
func TestShowRendersChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"i-9","ref":"TSK-9","title":"ship it","status":"in_progress","priority":"p1","kind":"feature",
"checks":[{"id":"c-1","title":"lists fast","runner":"human","status":"pending","created_at":"2026-08-01T00:00:00Z"}]}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"show", "TSK-9"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"lists fast", "pending"} {
		if !strings.Contains(got, want) {
			t.Fatalf("show output missing %q:\n%s", want, got)
		}
	}
}

// TestNextNeedsAHuman exercises `next`'s needs-a-human block: pending
// human-run checks the agent queue never ranks still surface, after the
// queue itself.
func TestNextNeedsAHuman(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/next":
			fmt.Fprint(w, `[{"issue_id":"i-1","ref":"TSK-1","title":"fix thing","status":"open","priority":"p1","score":1.5,"reasons":["ready"]}]`)
		case "/api/checks/pending":
			fmt.Fprint(w, `[{"issue_id":"i-3","issue_ref":"TSK-3","issue_title":"needs eyeballs","check_id":"c-1","title":"looks right in prod","runner":"human"}]`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"next"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "Needs a human") {
		t.Fatalf("want a needs-a-human block, got:\n%s", got)
	}
	if !strings.Contains(got, "TSK-3") {
		t.Fatalf("want the pending check's issue ref, got:\n%s", got)
	}
}

// TestNextSwallowsPendingChecksError exercises the courtesy-line rule: when
// the pending-checks fetch fails after the queue already printed, next
// still succeeds and prints the queue.
func TestNextSwallowsPendingChecksError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/next":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"issue_id":"i-1","ref":"TSK-1","title":"fix thing","status":"open","priority":"p1","score":1.5,"reasons":["ready"]}]`)
		case "/api/checks/pending":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"next"}, &out, &errb, env); code != 0 {
		t.Fatalf("want next to succeed despite the pending-checks failure, exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "TSK-1") {
		t.Fatalf("want the queue to still print, got:\n%s", out.String())
	}
	if errb.String() != "" {
		t.Fatalf("want no error printed, got stderr:\n%s", errb.String())
	}
}

// TestCheckRun exercises `check run` end to end: --pass and --measure land
// on the wire as outcome=pass with the parsed measurement, and the printed
// line names the outcome and the run id.
func TestCheckRun(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"r-1","outcome":"pass"}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"check", "run", "c-1", "--pass", "--measure", "list.rps=462r/s", "--conditions", "c50", "--sha", "abc"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotPath != "/api/checks/c-1/runs" {
		t.Fatalf("want POST /api/checks/c-1/runs, got %s", gotPath)
	}
	for _, want := range []string{`"outcome":"pass"`, `"metric":"list.rps"`, `"value":462`, `"unit":"r/s"`, `"conditions":"c50"`, `"head_sha":"abc"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("request body missing %q, got %s", want, gotBody)
		}
	}
	got := out.String()
	if !strings.Contains(got, "pass") || !strings.Contains(got, "r-1") {
		t.Fatalf("want the outcome and run id printed, got:\n%s", got)
	}
}
