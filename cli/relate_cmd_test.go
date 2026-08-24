// cli/relate_cmd_test.go
package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRelateWireShape exercises `taskr relate` end to end: it posts to
// /api/issues/{ref}/relate with to, type and remove:false all present, and
// the printed line states the edge in the direction it reads.
func TestRelateWireShape(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x", "TASKR_SESSION": "sess-1"})
	args := []string{"relate", "TSK-102", "BLOCKS", "TSK-103"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/issues/TSK-102/relate" {
		t.Fatalf("want POST /api/issues/TSK-102/relate, got %s %s", gotMethod, gotPath)
	}
	for _, want := range []string{`"to":"TSK-103"`, `"type":"BLOCKS"`, `"remove":false`, `"session_id":"sess-1"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body is missing %s, got %s", want, gotBody)
		}
	}
	if got := out.String(); !strings.Contains(got, "TSK-102 BLOCKS TSK-103") {
		t.Fatalf("want the edge printed in the direction it reads, got:\n%s", got)
	}
}

// TestUnrelateWireShape mirrors TestRelateWireShape: unrelate hits the
// same endpoint with remove:true rather than a second path.
func TestUnrelateWireShape(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"unrelate", "TSK-102", "BLOCKS", "TSK-103"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/api/issues/TSK-102/relate" {
		t.Fatalf("want POST /api/issues/TSK-102/relate, got %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"remove":true`) {
		t.Fatalf("want remove:true in the request body, got %s", gotBody)
	}
	if got := out.String(); !strings.Contains(got, "TSK-102 BLOCKS TSK-103") {
		t.Fatalf("want the removed edge named, got:\n%s", got)
	}
}

// TestRelateLowercaseTypeAccepted exercises the case-insensitivity rule:
// an agent typing `blocks` gets BLOCKS on the wire, not a 400.
func TestRelateLowercaseTypeAccepted(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	args := []string{"relate", "TSK-102", "blocks", "TSK-103"}
	if code := Run(args, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(gotBody, `"type":"BLOCKS"`) {
		t.Fatalf("want type upper-cased to BLOCKS on the wire, got %s", gotBody)
	}
}

// TestRelateRefusesGroupVerbs exercises the local refusal: PARENT_OF and
// CHILD_OF are real values the aggregate still refuses with
// ErrUseGroupVerbs, so the CLI catches both before the round trip — a
// server hit here would be the bug this test is watching for.
func TestRelateRefusesGroupVerbs(t *testing.T) {
	for _, relType := range []string{"PARENT_OF", "child_of"} {
		t.Run(relType, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected request %s %s — the refusal must not reach the server", r.Method, r.URL.Path)
			}))
			defer srv.Close()

			var out, errb bytes.Buffer
			env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
			code := Run([]string{"relate", "TSK-102", relType, "TSK-103"}, &out, &errb, env)
			if code == 0 {
				t.Fatalf("want non-zero exit for %s, got 0", relType)
			}
			if !strings.Contains(errb.String(), "group add") || !strings.Contains(errb.String(), "group rm") {
				t.Fatalf("want the group verbs named in the error, got: %s", errb.String())
			}
		})
	}
}

// TestRelateRefusesUnknownType exercises the other local refusal: a type
// the server would never accept is caught here, naming the legal values,
// rather than costing a round trip to learn as a 400.
func TestRelateRefusesUnknownType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request %s %s — the refusal must not reach the server", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	code := Run([]string{"relate", "TSK-102", "SUPERSEDES", "TSK-103"}, &out, &errb, env)
	if code == 0 {
		t.Fatalf("want non-zero exit for an unknown type, got 0")
	}
	if !strings.Contains(errb.String(), "BLOCKS") {
		t.Fatalf("want the legal types named in the error, got: %s", errb.String())
	}
}

// TestRelateAndUnrelateAppearInTheHelpText pins the affordance is
// discoverable — the whole point of TSK-108 is that an agent reaching for
// `taskr --help` finds a verb for this instead of a hand-rolled PATCH.
func TestRelateAndUnrelateAppearInTheHelpText(t *testing.T) {
	var out, errb bytes.Buffer
	Run([]string{"--help"}, &out, &errb, envAt(map[string]string{}))
	text := out.String() + errb.String()
	for _, want := range []string{"taskr relate", "taskr unrelate"} {
		if !strings.Contains(text, want) {
			t.Errorf("help text does not mention `%s`:\n%s", want, text)
		}
	}
}
