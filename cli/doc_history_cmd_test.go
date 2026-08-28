// cli/doc_history_cmd_test.go
package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDocHistory exercises `doc history` end to end: it reads the document's
// revisions endpoint and renders one row per revision, oldest first, with
// the diff summaries that `doc add --diff` wrote — the summaries the
// projector stored and nothing, before this verb, ever read back.
func TestDocHistory(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"revision":1,"sha256":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","revised_at":"2026-08-27T10:00:00Z"},
			{"revision":2,"sha256":"f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5","diff_summary":"reworked the rollout steps","revised_at":"2026-08-28T09:30:00Z"}
		]`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"doc", "history", "doc-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotPath != "/api/documents/doc-1/revisions" {
		t.Fatalf("want GET /api/documents/doc-1/revisions, got %s", gotPath)
	}
	got := out.String()
	for _, want := range []string{
		"doc-1",
		"2 revision(s)",
		"a1b2c3d4e5f6",
		"f6e5d4c3b2a1",
		"reworked the rollout steps",
		"2026-08-27",
		"2026-08-28",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("doc history output missing %q:\n%s", want, got)
		}
	}
}

// TestDocHistoryUsageErrorsWithoutAnID — a bare `doc history` names the
// usage rather than issuing a request for an empty id.
func TestDocHistoryUsageErrorsWithoutAnID(t *testing.T) {
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": "http://127.0.0.1:1", "TASKR_KEY": "x"})
	if code := Run([]string{"doc", "history"}, &out, &errb, env); code == 0 {
		t.Fatal("bare doc history succeeded, want a usage error")
	}
	if !strings.Contains(errb.String(), "usage: taskr doc history <id>") {
		t.Fatalf("stderr = %q, want the usage line", errb.String())
	}
}
