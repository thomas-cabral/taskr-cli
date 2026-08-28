// cli/catchup_cmd_test.go
package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const catchupBody = `{
  "ref":"TSK-212","title":"webhook retries drop on redelivery",
  "dead_ends":[
    {"kind":"ruled_out","step":"2. try the webhook-first approach",
     "reason":"the signature is computed before the retry envelope exists",
     "actor":"agent","at":"2026-08-18T10:00:00Z","head_sha":"bbb2222"},
    {"kind":"dropped","step":"4. backfill historical deliveries",
     "reason":"the ledger is write-through","actor":"agent","at":"2026-08-18T11:00:00Z"}],
  "plan":[
    {"position":3,"title":"add the retry ledger","status":"in_progress","note":"wiring the writer"},
    {"position":5,"title":"document it","status":"pending"}],
  "state":{"status":"open","kind":"bug","priority":"critical","progress":"2/4 done",
           "branch":"feat/retries","head_sha":"ccc3333"},
  "history":[
    {"actor":"agent","date":"2026-08-17","summary":"opened; planned 5 steps"},
    {"date":"2026-08-19","through":"2026-08-27","sessions":9,"summary":"3 comments","head_sha":"ccc3333"}],
  "evidence":[{"repo":"taskr","branch":"feat/retries","from":"aaa1111","to":"ccc3333","at":"2026-08-27T10:00:00Z"}],
  "budget":{"budget":2000,"estimated":420,"elided_sessions":14,
            "notice":"+14 earlier sessions elided to stay under 2000 tokens; ` + "`taskr timeline TSK-212`" + ` has the full stream"}
}`

// catchupServer answers GET /api/issues/{ref}/catchup with body, recording
// the query it was asked with.
func catchupServer(t *testing.T, body string, seen *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/catchup") {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		*seen = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

func runCatchup(t *testing.T, srv *httptest.Server, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run(append([]string{"catchup"}, args...), &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	return out.String()
}

// The dead ends are the reason this command exists, so they print before
// the plan and before the history — anything printed after the reader has
// started deciding is read too late.
func TestCatchupPrintsDeadEndsFirst(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, catchupBody, &seen)
	defer srv.Close()

	got := runCatchup(t, srv, "TSK-212")

	ruled := strings.Index(got, "Already ruled out")
	plan := strings.Index(got, "Still to do")
	history := strings.Index(got, "How it got here")
	if ruled < 0 || plan < 0 || history < 0 {
		t.Fatalf("missing a section:\n%s", got)
	}
	if !(ruled < plan && plan < history) {
		t.Errorf("sections out of order (ruled %d, plan %d, history %d):\n%s", ruled, plan, history, got)
	}
	for _, want := range []string{
		"2. try the webhook-first approach",
		"the signature is computed before the retry envelope exists",
		"4. backfill historical deliveries",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// The anchor is the commit an agent will actually stand on, so it has to
// be in the output and it has to be the one the packet named.
func TestCatchupPrintsTheAnchorAndTheElisionNotice(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, catchupBody, &seen)
	defer srv.Close()

	got := runCatchup(t, srv, "TSK-212")

	if !strings.Contains(got, "feat/retries @ ccc3333") {
		t.Errorf("output does not name the last commit seen:\n%s", got)
	}
	if !strings.Contains(got, "plan: 2/4 done") {
		t.Errorf("output does not carry the plan position:\n%s", got)
	}
	// A caller given less than the whole story has to be told, or they act
	// on a partial history believing it complete.
	if !strings.Contains(got, "+14 earlier sessions elided") {
		t.Errorf("elision notice missing:\n%s", got)
	}
}

// A coalesced run of days is one line and says how many sessions it stands
// for, so a reader can tell "nine quiet days" from "one day".
func TestCatchupRendersACoalescedRun(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, catchupBody, &seen)
	defer srv.Close()

	got := runCatchup(t, srv, "TSK-212")
	if !strings.Contains(got, "2026-08-19..2026-08-27") || !strings.Contains(got, "×9 sessions") {
		t.Errorf("coalesced run not rendered as a range with its session count:\n%s", got)
	}
}

// Layer 3 prints as the command the reader runs. Shipping addresses only
// works if the address is one paste away from being an answer.
func TestCatchupRendersEvidenceAsAGitCommand(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, catchupBody, &seen)
	defer srv.Close()

	got := runCatchup(t, srv, "TSK-212")
	// The whole line has to paste into a shell, so the command comes first
	// and the provenance sits behind a comment marker.
	if !strings.Contains(got, "  git log aaa1111..ccc3333   # taskr, feat/retries") {
		t.Errorf("evidence not rendered as a pasteable command:\n%s", got)
	}
}

// The flags reach the server as query parameters, and a budget of zero is
// omitted rather than sent — the server's default per layer is the point
// of leaving it unset.
func TestCatchupSendsItsFlagsAsQuery(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, catchupBody, &seen)
	defer srv.Close()

	runCatchup(t, srv, "TSK-212")
	if seen.Has("budget") || seen.Has("deep") {
		t.Errorf("bare catchup sent %v, want neither budget nor deep", seen)
	}

	runCatchup(t, srv, "TSK-212", "--budget", "500", "--deep")
	if seen.Get("budget") != "500" || seen.Get("deep") != "1" {
		t.Errorf("query = %v, want budget=500 and deep=1", seen)
	}
}

// --deep is the only layer that carries people's words, so the test that
// it renders them is the test that the drill-down is worth asking for.
func TestCatchupDeepRendersTheDecisionTrail(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, `{
	  "ref":"TSK-212","title":"x","state":{"status":"open","kind":"bug","priority":"high"},
	  "trail":{
	    "verdicts":[{"verdict":"actionable","evidence":"still reproduces at deliver/webhook.go:212","at":"2026-08-20T09:00:00Z"}],
	    "checks":[{"id":"c1","title":"redelivery keeps its budget","status":"pending","expect":"no dropped retries","runner":"agent","created_at":"2026-08-20T09:00:00Z"}],
	    "documents":[{"id":"d1","title":"catch-up design","type":"spec"}],
	    "comments":[{"id":"m1","actor":"human","body":"Ledger has the attempt boundary already.","created_at":"2026-08-20T10:00:00Z"}]},
	  "budget":{"budget":8000,"estimated":300}}`, &seen)
	defer srv.Close()

	got := runCatchup(t, srv, "TSK-212", "--deep")
	for _, want := range []string{
		"Triaged actionable",
		"still reproduces at deliver/webhook.go:212",
		"no dropped retries",
		"taskr doc show",
		"Ledger has the attempt boundary already.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("deep output missing %q:\n%s", want, got)
		}
	}
}

// Without --deep the trail is absent, which is what makes the default read
// cheap enough to run on every resume.
func TestCatchupWithoutDeepPrintsNoTrail(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, catchupBody, &seen)
	defer srv.Close()

	got := runCatchup(t, srv, "TSK-212")
	for _, absent := range []string{"Triaged", "Comments:", "Checks:", "Documents"} {
		if strings.Contains(got, absent) {
			t.Errorf("bare catchup printed %q, which is the --deep layer:\n%s", absent, got)
		}
	}
}

func TestCatchupJSONPassesThePacketThrough(t *testing.T) {
	var seen url.Values
	srv := catchupServer(t, catchupBody, &seen)
	defer srv.Close()

	got := runCatchup(t, srv, "TSK-212", "--json")
	if !strings.Contains(got, `"dead_ends"`) || !strings.Contains(got, `"elided_sessions"`) {
		t.Errorf("--json did not pass the packet through:\n%s", got)
	}
}

func TestCatchupWithoutARefIsAUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": "http://127.0.0.1:1", "TASKR_KEY": "x"})
	if code := Run([]string{"catchup"}, &out, &errb, env); code == 0 {
		t.Fatal("catchup with no ref succeeded, want a usage error")
	}
	if !strings.Contains(errb.String(), "usage: taskr catchup") {
		t.Errorf("stderr = %q, want the usage line", errb.String())
	}
}

// The resume packet is where the catch-up earns its keep: resuming is the
// moment an agent would otherwise reconstruct the history, and it happens
// without anyone having to know `taskr catchup` exists.
func TestResumePacketPrintsTheCatchupSection(t *testing.T) {
	packet := `{
	  "session":{"id":"s-1","machine":"box","status":"active","started_at":"2026-08-28T09:00:00Z"},
	  "issue":{"id":"i-1","ref":"TSK-212","title":"webhook retries drop on redelivery",
	           "status":"in_progress","priority":"high","kind":"bug","description":"",
	           "created_at":"2026-08-17T09:00:00Z","updated_at":"2026-08-28T09:00:00Z"},
	  "graph":{},
	  "catchup":{
	    "dead_ends":[{"kind":"ruled_out","step":"2. try the webhook-first approach",
	                  "reason":"the signature is computed before the retry envelope exists",
	                  "at":"2026-08-18T10:00:00Z"}],
	    "history":[{"date":"2026-08-17","summary":"opened; planned 5 steps"}],
	    "budget":{"budget":800,"estimated":120}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, packet)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"start", "TSK-212"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()

	if !strings.Contains(got, "Already ruled out") ||
		!strings.Contains(got, "the signature is computed before the retry envelope exists") {
		t.Errorf("resume packet does not carry the dead ends:\n%s", got)
	}
	if !strings.Contains(got, "How it got here") {
		t.Errorf("resume packet does not carry the collapsed history:\n%s", got)
	}
	// Above the tree state, because the dead ends answer the same question
	// the resume note does and anything below it is read after the agent
	// has started deciding.
	ruled, tree := strings.Index(got, "Already ruled out"), strings.Index(got, "Tree state")
	if tree >= 0 && ruled > tree {
		t.Errorf("dead ends printed below the tree state (ruled %d, tree %d):\n%s", ruled, tree, got)
	}
}

// A packet from a server that has no catch-up — an older instance, or an
// issue with nothing ruled out — renders exactly as it did before. The
// section is additive or it is a regression.
func TestResumePacketWithoutACatchupIsUnchanged(t *testing.T) {
	packet := `{
	  "session":{"id":"s-1","machine":"box","status":"active","started_at":"2026-08-28T09:00:00Z"},
	  "issue":{"id":"i-1","ref":"TSK-212","title":"x","status":"open","priority":"high","kind":"bug",
	           "description":"","created_at":"2026-08-17T09:00:00Z","updated_at":"2026-08-28T09:00:00Z"},
	  "graph":{}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, packet)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"start", "TSK-212"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	for _, absent := range []string{"Already ruled out", "How it got here"} {
		if strings.Contains(got, absent) {
			t.Errorf("packet with no catch-up printed %q:\n%s", absent, got)
		}
	}
	if !strings.Contains(got, "TSK-212") {
		t.Errorf("packet did not render at all:\n%s", got)
	}
}
