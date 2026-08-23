// cli/client_wire_test.go
package cli_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thomas-cabral/taskr-cli/cli"
)

func TestClientSetsBaseURLAndKeyHeaderUnconditionally(t *testing.T) {
	var gotAuth string
	// A bare httptest server that just records the header, so this test is
	// about the client's own request-building, independent of the real API.
	srv := httptest.NewServer(nil)
	srv.Config.Handler = recordingHandler(&gotAuth)
	defer srv.Close()

	c := &cli.Client{BaseURL: srv.URL, Key: ""}
	if _, err := c.AuthStatus(context.Background()); err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("X-Taskr-Key = %q, want empty string sent explicitly", gotAuth)
	}

	c.Key = "tk_abc"
	if _, err := c.AuthStatus(context.Background()); err != nil {
		t.Fatalf("AuthStatus: %v", err)
	}
	if gotAuth != "tk_abc" {
		t.Errorf("X-Taskr-Key = %q, want tk_abc", gotAuth)
	}
}

func TestEveryWriteCarriesAFreshIdempotencyKey(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(nil)
	srv.Config.Handler = idempotencyRecordingHandler(&seen)
	defer srv.Close()

	c := &cli.Client{BaseURL: srv.URL}
	if _, err := c.CreateIssue(context.Background(), cli.CreateIssueInput{Title: "a"}); err != nil {
		t.Fatalf("CreateIssue 1: %v", err)
	}
	if _, err := c.CreateIssue(context.Background(), cli.CreateIssueInput{Title: "b"}); err != nil {
		t.Fatalf("CreateIssue 2: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("saw %d writes, want 2", len(seen))
	}
	if seen[0] == "" || seen[1] == "" {
		t.Fatalf("Idempotency-Key headers = %q, want both non-empty", seen)
	}
	if seen[0] == seen[1] {
		t.Errorf("both writes carried the same Idempotency-Key %q, want distinct keys per call", seen[0])
	}
}

// TestAddChildPostsCorrectPathAndBody pins the wire shape behind `taskr
// group add`: a child attaches to its parent's collection at
// /children, with the child's ref as the only thing the body carries.
func TestAddChildPostsCorrectPathAndBody(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = strings.TrimSpace(string(b))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &cli.Client{BaseURL: srv.URL}
	if err := c.AddChild(context.Background(), "TSK-1", "TSK-2"); err != nil {
		t.Fatalf("AddChild: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/issues/TSK-1/children" {
		t.Errorf("path = %q, want /api/issues/TSK-1/children", gotPath)
	}
	if gotBody != `{"child":"TSK-2"}` {
		t.Errorf("body = %q, want {\"child\":\"TSK-2\"}", gotBody)
	}
}

// TestRemoveChildDeletesCorrectPath pins the wire shape behind `taskr group
// remove`: the child's ref names itself in the path, not a body — DELETE
// carries none.
func TestRemoveChildDeletesCorrectPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &cli.Client{BaseURL: srv.URL}
	if err := c.RemoveChild(context.Background(), "TSK-1", "TSK-2"); err != nil {
		t.Fatalf("RemoveChild: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/api/issues/TSK-1/children/TSK-2" {
		t.Errorf("path = %q, want /api/issues/TSK-1/children/TSK-2", gotPath)
	}
}

// TestUpdateIssueDecodesGroupHint pins that a close response's group_hint —
// the nudge toward a parent's next open child — survives the trip into
// UpdateIssueResult, nested NextChild included.
func TestUpdateIssueDecodesGroupHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"id-1","ref":"TSK-1","group_hint":{"parent_ref":"TSK-9",`+
			`"next_child":{"id":"id-2","ref":"TSK-2","title":"next up","status":"open","position":2},`+
			`"all_children_closed":false}}`)
	}))
	defer srv.Close()

	c := &cli.Client{BaseURL: srv.URL}
	out, err := c.UpdateIssue(context.Background(), "TSK-1", cli.UpdateIssueInput{Status: "closed"})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if out.ID != "id-1" || out.Ref != "TSK-1" {
		t.Errorf("out = %+v, want id-1/TSK-1", out)
	}
	if out.GroupHint == nil {
		t.Fatal("GroupHint = nil, want non-nil")
	}
	if out.GroupHint.ParentRef != "TSK-9" {
		t.Errorf("ParentRef = %q, want TSK-9", out.GroupHint.ParentRef)
	}
	if out.GroupHint.AllChildrenClosed {
		t.Errorf("AllChildrenClosed = true, want false")
	}
	if out.GroupHint.NextChild == nil {
		t.Fatal("NextChild = nil, want non-nil")
	}
	want := cli.GroupChild{ID: "id-2", Ref: "TSK-2", Title: "next up", Status: "open", Position: 2}
	if *out.GroupHint.NextChild != want {
		t.Errorf("NextChild = %+v, want %+v", *out.GroupHint.NextChild, want)
	}
}
