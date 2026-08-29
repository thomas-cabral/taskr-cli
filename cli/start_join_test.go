// cli/start_join_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What these hold is what a refusal is FOR. `start` on an issue somebody is
// live on does not fail because a rule says so; it fails because there is a
// person to talk to, and the only useful thing the CLI can do is name them
// and offer the two real answers. TSK-203, spec section a4.

// heldBody is the 409 the server answers with when a teammate's session is
// live on the issue.
const heldBody = `{"error":"TSK-1 is held by alice@example.com",
 "holders":[{"session_id":"s-1","user_id":"u-2","email":"alice@example.com","machine":"laptop","agent":"claude-code","cwd":"/home/alice/app","started_at":"2026-08-29T10:00:00Z","last_seen":"2026-08-29T11:50:00Z","own":false}]}`

// startServer refuses a start with 409 unless the request asked to join, in
// which case it answers a minimal packet. It records the last body it saw.
func startServer(t *testing.T, seen *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/work/start" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if seen != nil {
			*seen = body
		}
		w.Header().Set("Content-Type", "application/json")
		if join, _ := body["join"].(bool); !join {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, heldBody)
			return
		}
		fmt.Fprint(w, `{"session":{"id":"s-2","machine":"desktop","status":"active"},
		 "issue":{"id":"i-1","ref":"TSK-1","title":"the contended one","status":"in_progress","priority":"high","kind":"task"},
		 "holders":[{"session_id":"s-1","email":"alice@example.com","machine":"laptop","agent":"claude-code","started_at":"2026-08-29T10:00:00Z","last_seen":"2026-08-29T11:50:00Z","own":false}]}`)
	}))
}

// A refusal that said only "held" would leave the reader with nobody to ask.
// Every line of this message is there to be acted on: who, from where, and
// the two ways forward.
func TestStartOnAHeldIssueNamesTheHolderAndBothWaysForward(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := startServer(t, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"start", "TSK-1"}, &out, &errb, env); code == 0 {
		t.Fatalf("exit 0, want a refusal — stdout: %s", out.String())
	}
	text := errb.String()

	for _, want := range []string{"alice@example.com", "laptop", "/home/alice/app", "park -r handoff", "start TSK-1 --join"} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal is missing %q:\n%s", want, text)
		}
	}
	// The packet is not printed on a refusal: nothing was started.
	if strings.Contains(out.String(), "TSK-1") {
		t.Errorf("a refused start printed a resume packet:\n%s", out.String())
	}
}

// --join is the one-word answer when the two of them have already agreed. It
// travels on the request rather than being decided locally, and what comes
// back says who you joined — a join that named nobody would be the one thing
// joining was for, missing.
func TestStartJoinSendsTheFlagAndSaysWhoYouJoined(t *testing.T) {
	t.Chdir(t.TempDir())
	var seen map[string]any
	srv := startServer(t, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"start", "TSK-1", "--join"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if join, _ := seen["join"].(bool); !join {
		t.Errorf("request body = %+v, want join:true — the flag is what makes it deliberate", seen)
	}
	text := out.String()
	if !strings.Contains(text, "Working alongside") || !strings.Contains(text, "alice@example.com") {
		t.Errorf("joined packet does not say who you joined:\n%s", text)
	}
}

// A start that was never refused says nothing about anybody. This is the
// first-person rule: for one person working alone, none of this exists.
func TestStartAloneSaysNothingAboutHolders(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"session":{"id":"s-1","machine":"desktop","status":"active"},
		 "issue":{"id":"i-1","ref":"TSK-1","title":"mine alone","status":"in_progress","priority":"high","kind":"task"}}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"start", "TSK-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, unwanted := range []string{"Working alongside", "went stale", "held by"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("solo start printed %q:\n%s", unwanted, out.String())
		}
	}
}

// A stale session is not a holder and refuses nothing — but it is the reason
// there may be uncommitted work on a branch on another machine with no note
// anywhere saying so. The packet says it; the reader decides.
func TestStartRendersAStaleHolderAsAWarningNotARefusal(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"session":{"id":"s-2","machine":"desktop","status":"active"},
		 "issue":{"id":"i-1","ref":"TSK-1","title":"picked up cold","status":"in_progress","priority":"high","kind":"task"},
		 "stale_holders":[{"session_id":"s-1","email":"alice@example.com","machine":"laptop","agent":"claude-code","started_at":"2026-08-29T06:00:00Z","last_seen":"2026-08-29T07:00:00Z","own":false}]}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"start", "TSK-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	if !strings.Contains(text, "alice@example.com's session on this went stale") {
		t.Errorf("stale holder is not surfaced:\n%s", text)
	}
	if !strings.Contains(text, "laptop") {
		t.Errorf("the warning does not name the machine the work may be on:\n%s", text)
	}
}

// The other side of the same story: coming back to find the issue moved on.
// It prints only when both halves are true, so an ordinary quiet session
// sees nothing.
func TestContextSaysWhenTheClaimLapsedAndSomebodyElseTookTheIssue(t *testing.T) {
	t.Chdir(t.TempDir())
	view := `{"machine":"laptop","open_issues":3,
	 "active_session":{"id":"s-1","machine":"laptop","issue_id":"i-1","status":"active"},
	 "active_issue":{"id":"i-1","ref":"TSK-1","title":"the one that moved","status":"in_progress","priority":"high","kind":"task"},
	 "claim_lost":{"issue_ref":"TSK-1","stale_at":"2026-08-29T07:00:00Z",
	  "holders":[{"session_id":"s-2","email":"bob@example.com","machine":"desktop","agent":"claude-code","started_at":"2026-08-29T11:00:00Z","last_seen":"2026-08-29T11:55:00Z","own":false}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, view)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	for _, want := range []string{"claim lapsed", "TSK-1", "bob@example.com"} {
		if !strings.Contains(text, want) {
			t.Errorf("context is missing %q:\n%s", want, text)
		}
	}
}

// No claim_lost, no line. An older server sends the field not at all, and a
// current one omits it whenever the session is live or nobody else moved —
// which for one person is always.
func TestContextSaysNothingWithoutAClaimLost(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"machine":"laptop","open_issues":3,
		 "active_session":{"id":"s-1","machine":"laptop","issue_id":"i-1","status":"active"},
		 "active_issue":{"id":"i-1","ref":"TSK-1","title":"still mine","status":"in_progress","priority":"high","kind":"task"}}`)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "claim lapsed") {
		t.Errorf("a live session was told it lost its claim:\n%s", out.String())
	}
}
