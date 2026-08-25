// cli/edit_cmd_test.go
package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// editServer captures the PATCH cmdEdit sends and answers the way the
// server does: 200 with a ref.
func editServer(t *testing.T, gotBody *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("got %s, want PATCH", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		*gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"i-1","ref":"TSK-9"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runEdit(args []string, api string) (string, string, int) {
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": api, "TASKR_KEY": "x", "TASKR_SESSION": "sess-1"})
	code := Run(append([]string{"edit"}, args...), &out, &errb, env)
	return out.String(), errb.String(), code
}

// TestEditSendsOnlyTheGivenFields pins the partial-edit contract: fields
// the caller did not name are absent from the wire entirely, because the
// server treats empty as untouched and an accidental "" would blank them.
func TestEditSendsOnlyTheGivenFields(t *testing.T) {
	var gotBody string
	srv := editServer(t, &gotBody)

	out, _, code := runEdit([]string{"TSK-9", "--title", "renamed", "--priority", "high"}, srv.URL)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{`"title":"renamed"`, `"priority":"high"`, `"session_id":"sess-1"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %s, got %s", want, gotBody)
		}
	}
	if strings.Contains(gotBody, `"description"`) {
		t.Errorf("description sent although not asked for: %s", gotBody)
	}
	if strings.Contains(gotBody, `"status"`) {
		t.Errorf("status sent by an edit: %s", gotBody)
	}
	if !strings.Contains(out, "Updated TSK-9: title, priority.") {
		t.Errorf("stdout wrong:\n%s", out)
	}
}

// TestEditClearDescSendsEmptyString pins the one deliberate exception to
// empty-means-untouched: --clear-desc must send description:"" so the
// server clears the brief rather than ignoring the request.
func TestEditClearDescSendsEmptyString(t *testing.T) {
	var gotBody string
	srv := editServer(t, &gotBody)

	if _, _, code := runEdit([]string{"TSK-9", "--clear-desc"}, srv.URL); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(gotBody, `"description":""`) {
		t.Errorf("body missing explicit empty description, got %s", gotBody)
	}
}

// TestEditRefusesContradictionsAndNoOps pins the local refusals before any
// wire traffic: --desc with --clear-desc contradicts itself, and an edit
// naming no field has nothing to do.
func TestEditRefusesContradictionsAndNoOps(t *testing.T) {
	var gotBody string
	srv := editServer(t, &gotBody)

	cases := [][]string{
		{"TSK-9"},
		{"TSK-9", "--desc", "body", "--clear-desc"},
	}
	for _, args := range cases {
		out, errb, code := runEdit(args, srv.URL)
		if code == 0 {
			t.Errorf("edit %v exited 0, want refusal; stdout:\n%s", args, out)
		}
		if gotBody != "" {
			t.Errorf("edit %v sent a request body %q despite refusing", args, gotBody)
		}
		if !strings.Contains(errb+out, "usage") && !strings.Contains(errb+out, "contradict") && !strings.Contains(errb+out, "nothing to change") {
			t.Errorf("edit %v refused without saying why:\n%s%s", args, out, errb)
		}
	}
}

// TestEditDescAndTitleTogether exercises the pointer-vs-string split on one
// call: a new description rides alongside a renamed title.
func TestEditDescAndTitleTogether(t *testing.T) {
	var gotBody string
	srv := editServer(t, &gotBody)

	out, _, code := runEdit([]string{"TSK-9", "--title", "t2", "--desc", "fresh brief"}, srv.URL)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{`"title":"t2"`, `"description":"fresh brief"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %s, got %s", want, gotBody)
		}
	}
	if !strings.Contains(out, "title, description") {
		t.Errorf("stdout wrong:\n%s", out)
	}
}
