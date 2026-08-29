// cli/next_held_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// What these hold is the first-person rule, which is easier to break than to
// state: your queue gets QUIETER when a teammate is working, never louder.
// One line when there is something to say, nothing at all when there is not,
// and the rows themselves only when you ask. TSK-238, spec section a3.

// nextBody is one ready candidate followed by one the server appended
// because a teammate is live on it — the shape GET /api/next answers with
// held=1.
const nextBody = `[
	{"issue_id":"i-1","ref":"TSK-1","title":"free to take","status":"open","priority":"high","score":7,"reasons":["unblocked","high priority"]},
	{"issue_id":"i-2","ref":"TSK-2","title":"a teammate is on this","status":"in_progress","priority":"high","score":6,"reasons":["unblocked"],
	 "held":[{"session_id":"s-1","user_id":"u-2","email":"alice@example.com","machine":"laptop","agent":"claude-code","started_at":"2026-08-29T10:00:00Z","last_seen":"2026-08-29T11:50:00Z","own":false}]}
]`

// nextServer answers /api/next with body and records the query it was asked.
func nextServer(t *testing.T, body string, seen *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/next":
			if seen != nil {
				q := r.URL.Query()
				*seen = q
			}
			fmt.Fprint(w, body)
		case "/api/checks/pending":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

// The default render subtracts held issues from the page and replaces them
// with one line. Printing the row itself would be the opposite of the rule:
// a teammate's work would be sitting in the reader's queue.
func TestNextOmitsHeldRowsAndCountsThemInOneLine(t *testing.T) {
	t.Chdir(t.TempDir())
	var q url.Values
	srv := nextServer(t, nextBody, &q)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"next"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()

	if !strings.Contains(text, "TSK-1") {
		t.Errorf("the ready candidate is missing:\n%s", text)
	}
	// The held row's ref must not appear in the table. The count line names
	// no refs, so a bare mention of TSK-2 means it was rendered as ready.
	if strings.Contains(text, "TSK-2") {
		t.Errorf("a teammate's held issue is in the ready queue:\n%s", text)
	}
	if !strings.Contains(text, "1 ready issue is held by teammates (next --held)") {
		t.Errorf("no count line for the held issue:\n%s", text)
	}
	// One request answers both. Asking a second time would double the cost
	// of every `next` a person runs.
	if q.Get("held") != "1" {
		t.Errorf("next did not ask for held rows: %v", q)
	}
}

// --held is the room you walk into, and it says who to ask rather than only
// that somebody is there.
func TestNextHeldRendersTheHoldersBlock(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := nextServer(t, nextBody, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"next", "--held"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()

	for _, want := range []string{
		"Held by teammates (1)",
		"TSK-2 — a teammate is on this",
		"held by alice@example.com",
		"laptop",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("--held output is missing %q:\n%s", want, text)
		}
	}
	// The count line and the block are the same fact at two detail levels;
	// printing both reads as a stutter.
	if strings.Contains(text, "(next --held)") {
		t.Errorf("--held still printed the hint that tells you to run --held:\n%s", text)
	}
}

// For one person the held list is always empty — their own sessions are
// never somebody else's claim — so this line must not exist in the solo
// output at all. It is the difference between a tool that stays out of the
// way and one that reminds you it has a team feature.
func TestNextPrintsNothingAboutHoldersForOneUser(t *testing.T) {
	t.Chdir(t.TempDir())
	solo := `[{"issue_id":"i-1","ref":"TSK-1","title":"free to take","status":"open","priority":"high","score":7,"reasons":["unblocked"]}]`
	srv := nextServer(t, solo, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"next"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if text := out.String(); strings.Contains(text, "held") || strings.Contains(text, "teammate") {
		t.Errorf("solo output mentions holders:\n%s", text)
	}
}

// --json is a machine contract. A script reading the array as ready work
// must not silently start receiving issues somebody else is on; it gets them
// when it asks, and then they carry the field that says so.
func TestNextJSONOnlyCarriesHeldRowsWhenAsked(t *testing.T) {
	t.Chdir(t.TempDir())
	var q url.Values
	srv := nextServer(t, nextBody, &q)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"next", "--json"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if q.Get("held") == "1" {
		t.Error("--json asked for held rows nobody requested")
	}

	out.Reset()
	if code := Run([]string{"next", "--json", "--held"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if q.Get("held") != "1" {
		t.Error("--json --held did not ask for held rows")
	}
	var rows []Candidate
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("--json output is not a candidate array: %v\n%s", err, out.String())
	}
	// Unfiltered and untouched: the consumer decides, and the field is how
	// it tells the two kinds apart.
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 — --json must pass the answer through", len(rows))
	}
	if len(rows[1].Held) != 1 || rows[1].Held[0].Email != "alice@example.com" {
		t.Errorf("held field did not survive --json: %+v", rows[1].Held)
	}
}

// A holder the server could not name is still a holder. Dropping the row
// would hide a live claim; naming nobody would be a lie. The machine and the
// agent are what that session actually knows about itself.
func TestHeldRowNamesTheMachineWhenItCannotNameThePerson(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	line := holderLine(now, []Holder{{
		SessionID: "s-1", Machine: "laptop", Agent: "claude-code",
		StartedAt: "2026-08-29T10:00:00Z",
	}})
	if !strings.Contains(line, "laptop · claude-code") {
		t.Errorf("holderLine = %q, want the machine and agent when there is no email", line)
	}
	if !strings.Contains(line, "started 2h ago") {
		t.Errorf("holderLine = %q, want how long they have been on it", line)
	}
}
