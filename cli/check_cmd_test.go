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
