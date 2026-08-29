// cli/neighbours_held_test.go
package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A suggestion that says only "this looks like that" leaves the reader to
// find out the expensive way that somebody is writing the other one right
// now. These hold the one clause that changes what the reader does next.
// TSK-204, spec section a5.

// heldNeighbor is the shape every neighbour surface answers with once a
// teammate's session is live on the suggestion.
const heldNeighbor = `{"id":"i-9","ref":"TSK-70","title":"auth cookie fallthrough","status":"open","score":0.84,
 "held":{"session_id":"s-1","email":"alice@example.com","machine":"laptop","agent":"claude-code","started_at":"2026-08-29T10:00:00Z","last_seen":"2026-08-29T11:50:00Z","own":false}}`

// Filing is where it matters most: the issue is already filed when the block
// prints, and a held twin turns "check whether this is a duplicate" into
// "comment on theirs, do not start yours".
func TestOffloadNamesWhoIsOnTheTwin(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/offload":
			fmt.Fprintf(w, `{"issue":{"id":"i-1","ref":"TSK-99"},"project_slug":"taskr","similar":[%s]}`, heldNeighbor)
		case "/api/context":
			fmt.Fprint(w, `{"machine":"laptop","open_issues":1}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"offload", "a thing", "-m", "a brief"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	if !strings.Contains(text, "TSK-70") {
		t.Fatalf("the twin is missing:\n%s", text)
	}
	if !strings.Contains(text, "held by alice@example.com") {
		t.Errorf("the twin does not say who is on it:\n%s", text)
	}
}

// Starting: both halves of the neighbourhood carry it. A blocker somebody is
// on right now is the answer to "how long until I am unblocked".
func TestStartAnnotatesBlockersAndSuggestionsWithHolders(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"session":{"id":"s-2","machine":"desktop","status":"active"},
		 "issue":{"id":"i-1","ref":"TSK-1","title":"the subject","status":"in_progress","priority":"high","kind":"task"},
		 "graph":{"issue_id":"i-1","blocked_by":[{"id":"i-9","ref":"TSK-70","title":"auth cookie fallthrough","status":"in_progress","type":"BLOCKED_BY","depth":1,
		   "held":{"session_id":"s-1","email":"alice@example.com","machine":"laptop","agent":"claude-code","started_at":"2026-08-29T10:00:00Z","last_seen":"2026-08-29T11:50:00Z","own":false}}]},
		 "similar":[%s]}`, heldNeighbor)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"start", "TSK-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	if !strings.Contains(text, "Similar open issues") {
		t.Errorf("the packet dropped its suggestions:\n%s", text)
	}
	// Twice: once on the blocker, once on the suggestion.
	if n := strings.Count(text, "held by alice@example.com"); n != 2 {
		t.Errorf("held annotations = %d, want 2 (the blocker and the suggestion):\n%s", n, text)
	}
}

// Triage: the queue is a table, so the row says only that the twin is held;
// the name belongs in `taskr triage <ref>`, where the twin renders in full.
func TestTriageQueueMarksAHeldTwin(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"issue_id":"i-1","issue_ref":"TSK-1","title":"needs a verdict","reason":"new","twin":%s}]`, heldNeighbor)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"triage"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	if !strings.Contains(text, "0.84 TSK-70 · held") {
		t.Errorf("the queue does not mark the held twin:\n%s", text)
	}
}

// None of it exists for one person working alone. A neighbour with no
// holder — which is every neighbour a solo contributor ever sees — renders
// exactly as it did before any of this.
func TestNeighboursWithNoHolderRenderUnchanged(t *testing.T) {
	var b strings.Builder
	RenderSimilar(&b, []Neighbor{{Ref: "TSK-70", Title: "auth cookie fallthrough", Score: 0.84}}, "")
	if strings.Contains(b.String(), "held") {
		t.Errorf("an unheld suggestion mentions holding:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "0.84  TSK-70") {
		t.Errorf("the unheld render changed shape:\n%s", b.String())
	}
}
