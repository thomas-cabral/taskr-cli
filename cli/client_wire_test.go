// cli/client_wire_test.go
package cli_test

import (
	"context"
	"encoding/json"
	"errors"
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

// TestRelateIssuesPostsCorrectBody pins the wire shape behind `taskr
// relate`: POST /api/issues/{ref}/relate with to, type and remove all
// present on the body — remove:false included explicitly, not omitted,
// since that is relate's own meaningful value.
func TestRelateIssuesPostsCorrectBody(t *testing.T) {
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
	in := cli.RelateInput{To: "TSK-2", Type: "BLOCKS", Remove: false, SessionID: "sess-1"}
	if err := c.RelateIssues(context.Background(), "TSK-1", in); err != nil {
		t.Fatalf("RelateIssues: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/issues/TSK-1/relate" {
		t.Errorf("path = %q, want /api/issues/TSK-1/relate", gotPath)
	}
	want := `{"to":"TSK-2","type":"BLOCKS","remove":false,"session_id":"sess-1"}`
	if gotBody != want {
		t.Errorf("body = %q, want %q", gotBody, want)
	}
}

// TestRelateIssuesRemoveTrueDeletesTheEdge pins the other half: remove
// rides the same endpoint and the same body shape, just flipped, rather
// than a second path taskr unrelate hits.
func TestRelateIssuesRemoveTrueDeletesTheEdge(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = strings.TrimSpace(string(b))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &cli.Client{BaseURL: srv.URL}
	in := cli.RelateInput{To: "TSK-2", Type: "BLOCKS", Remove: true, SessionID: "sess-1"}
	if err := c.RelateIssues(context.Background(), "TSK-1", in); err != nil {
		t.Fatalf("RelateIssues: %v", err)
	}
	if !strings.Contains(gotBody, `"remove":true`) {
		t.Errorf("body = %q, want remove:true", gotBody)
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

func TestDefineCheckPostsAndDecodes(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"c-1","issue":{"id":"i-1","ref":"TSK-9"}}`))
	}))
	defer srv.Close()
	c := &cli.Client{BaseURL: srv.URL}
	ref, err := c.DefineCheck(context.Background(), "TSK-9", cli.DefineCheckInput{
		Title: "lists fast", Procedure: "hey -z 30s", Expect: "> 100 r/s", Runner: "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /api/issues/TSK-9/checks" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"runner":"human"`) || !strings.Contains(gotBody, `"procedure":"hey -z 30s"`) {
		t.Fatalf("body = %s", gotBody)
	}
	if ref.ID != "c-1" || ref.Issue.Ref != "TSK-9" {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestRunCheckPostsMeasurements(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"42","outcome":"pass","recorded_at":"2026-08-23T00:00:00.000000000Z"}`))
	}))
	defer srv.Close()
	c := &cli.Client{BaseURL: srv.URL}
	run, err := c.RunCheck(context.Background(), "c-1", cli.RunCheckInput{
		Outcome:      "pass",
		Measurements: []cli.Measurement{{Metric: "list.rps", Value: 462, Unit: "r/s", Conditions: "c50"}},
		HeadSHA:      "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /api/checks/c-1/runs" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"metric":"list.rps"`) || !strings.Contains(gotBody, `"value":462`) {
		t.Fatalf("body = %s", gotBody)
	}
	if run.ID != "42" || run.Outcome != "pass" {
		t.Fatalf("run = %+v", run)
	}
}

func TestListChecksAndPendingChecksGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/issues/TSK-9/checks":
			w.Write([]byte(`[{"id":"c-1","title":"t","runner":"agent","status":"pending","created_at":"x"}]`))
		case "/api/checks/pending":
			if r.URL.Query().Get("runner") != "human" || r.URL.Query().Get("all") != "1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte(`[{"issue_id":"i","issue_ref":"TSK-9","issue_title":"T","check_id":"c-1","title":"t","runner":"human"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &cli.Client{BaseURL: srv.URL}
	checks, err := c.ListChecks(context.Background(), "TSK-9")
	if err != nil || len(checks) != 1 || checks[0].Status != "pending" {
		t.Fatalf("checks = %+v, %v", checks, err)
	}
	pending, err := c.PendingChecks(context.Background(), "human", cli.Locator{}, true)
	if err != nil || len(pending) != 1 || pending[0].IssueRef != "TSK-9" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}

func TestAPIErrorCarriesTheRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"pending","pending_checks":[{"id":"c-1","title":"t","runner":"human"}]}`))
	}))
	defer srv.Close()
	c := &cli.Client{BaseURL: srv.URL}
	_, err := c.UpdateIssue(context.Background(), "TSK-9", cli.UpdateIssueInput{Status: "closed"})
	var apiErr *cli.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusConflict {
		t.Fatalf("status = %d", apiErr.Status)
	}
	var body cli.PendingChecksBody
	if jsonErr := json.Unmarshal(apiErr.Body, &body); jsonErr != nil {
		t.Fatalf("Body not preserved: %v (%s)", jsonErr, apiErr.Body)
	}
	if len(body.PendingChecks) != 1 || body.PendingChecks[0].Runner != "human" {
		t.Fatalf("decoded = %+v", body)
	}
}

// TestListIssuesDecodesAgentEnvelopeAndLegacyArray pins the two wire shapes
// ListIssues must survive (TSK-137): the versioned agent envelope from the
// layered-search server, and the bare row array every pre-agent-search
// server still answers with. The first byte tells them apart; getting it
// backwards turns a search into a decode error for the whole CLI.
func TestListIssuesDecodesAgentEnvelopeAndLegacyArray(t *testing.T) {
	envelope := `{"q":"rewnds","layers":{"exact":0,"fuzzy":1},` +
		`"didyoumean":["rewind"],"expand":["status=open"],` +
		`"results":[{"ref":"TSK-9","title":"Replay loop","layer":"fuzzy","score":0.5}],` +
		`"more":false,"v":1}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "agent" {
			t.Errorf("request missing format=agent: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, envelope)
	}))
	defer srv.Close()

	c := &cli.Client{BaseURL: srv.URL}
	env, err := c.ListIssues(context.Background(), "rewnds", nil, cli.Locator{}, false)
	if err != nil {
		t.Fatalf("ListIssues envelope: %v", err)
	}
	if env.V != 1 || env.Q != "rewnds" {
		t.Errorf("envelope = v%d q%q, want v1 q rewnds", env.V, env.Q)
	}
	if len(env.DidYouMean) != 1 || env.DidYouMean[0] != "rewind" {
		t.Errorf("didyoumean = %v, want [rewind]", env.DidYouMean)
	}
	if len(env.Results) != 1 || env.Results[0].Layer != "fuzzy" {
		t.Errorf("results = %+v, want one fuzzy row", env.Results)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"ref":"TSK-3","title":"legacy row"}]`)
	}))
	defer srv2.Close()

	c2 := &cli.Client{BaseURL: srv2.URL}
	old, err := c2.ListIssues(context.Background(), "", nil, cli.Locator{}, false)
	if err != nil {
		t.Fatalf("ListIssues legacy array: %v", err)
	}
	if old.V != 0 || len(old.Results) != 1 || old.Results[0].Ref != "TSK-3" {
		t.Errorf("legacy decode = %+v, want version-0 envelope carrying the bare row", old)
	}
}
