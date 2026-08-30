// cli/team_cmd_test.go
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

// `taskr team` is the one command here written for a person: every other
// read subtracts other people's work, and this one is nothing but. What
// these hold is that each of the four blocks says the thing somebody reads
// it for. TSK-205, spec section b.

const teamBody = `{
  "on_it_now":[{"session_id":"s-1","email":"alice@example.com","machine":"laptop","agent":"claude-code","cwd":"/home/alice/app",
                "issue_ref":"TSK-1","issue_title":"in flight","branch":"feat/x",
                "started_at":"2026-08-29T10:00:00Z","last_seen":"2026-08-29T11:55:00Z","own":false}],
  "gone_quiet":[{"session_id":"s-2","email":"bob@example.com","machine":"desktop","agent":"claude-code",
                 "issue_ref":"TSK-2","issue_title":"went dark",
                 "started_at":"2026-08-29T06:00:00Z","last_seen":"2026-08-29T07:00:00Z","own":true}],
  "waiting_for_pickup":[{"id":"s-3","machine":"laptop","agent":"claude-code","issue_ref":"TSK-3","issue_title":"left mid-change",
                         "reason":"interrupted","parked_at":"2026-08-29T08:00:00Z","resume_note":"auto-park (session ended): feat/y @ abc123",
                         "email":"alice@example.com","own":false,"auto":true,"dirty_files":2,"branch":"feat/y"}],
  "recent_parks":[{"id":"s-4","machine":"desktop","agent":"claude-code","issue_ref":"TSK-4","issue_title":"stopped for the day",
                   "reason":"done_for_now","parked_at":"2026-08-29T09:00:00Z","email":"bob@example.com","own":true}]
}`

func teamServer(t *testing.T, body string, seen *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/team" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			return
		}
		if seen != nil {
			*seen = r.URL.Query()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

func TestTeamRendersTheFourBlocks(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := teamServer(t, teamBody, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"team"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()

	for _, want := range []string{
		"On it now (1)", "Gone quiet (1)", "Waiting for pickup (1)", "Recent parks (1)",
		"TSK-1 in flight", "alice@example.com", "feat/x @ laptop",
		// The auto-park warning, and why it is the row worth interrupting
		// somebody over.
		"Nobody wrote this note", "2 uncommitted files on laptop",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("team output is missing %q:\n%s", want, text)
		}
	}
	// Your own rows say "you". Reading your own address back at you is how a
	// board stops feeling like it is about people.
	if !strings.Contains(text, "TSK-2 went dark — you") {
		t.Errorf("the reader's own stale session is not addressed as theirs:\n%s", text)
	}
}

// Empty is a sentence, not four empty headings.
func TestTeamOnAQuietOrgSaysSoInOneLine(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := teamServer(t, `{"on_it_now":[],"gone_quiet":[],"waiting_for_pickup":[],"recent_parks":[]}`, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"team"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Nobody is working right now") {
		t.Errorf("quiet org output = %q", out.String())
	}
	if strings.Contains(out.String(), "On it now") {
		t.Errorf("an empty block printed a heading:\n%s", out.String())
	}
}

// --all widens past the project the caller is standing in, the same word it
// means everywhere else in this CLI.
func TestTeamAllWidensPastTheCurrentProject(t *testing.T) {
	t.Chdir(t.TempDir())
	var q url.Values
	srv := teamServer(t, teamBody, &q)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"team", "--all"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if q.Get("all") != "1" {
		t.Errorf("query = %v, want all=1", q)
	}
}

// A holder the server could not name renders as its machine rather than
// being dropped: "somebody on laptop" is still the fact the reader needs.
// The machine stands in for the person there, so a row must not then repeat
// it as the box the work is on — "buildbox · last seen 1h ago · buildbox"
// (TSK-241). A row that DOES name a person still ends with its machine.
func TestTeamNamesAnUnidentifiedSessionByItsMachine(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := teamServer(t, `{"on_it_now":[{"session_id":"s-1","machine":"buildbox","agent":"claude-code",
	 "issue_ref":"TSK-9","issue_title":"nameless","started_at":"2026-08-29T10:00:00Z","last_seen":"2026-08-29T11:00:00Z","own":false}],
	 "gone_quiet":[{"session_id":"s-2","machine":"buildbox","agent":"claude-code",
	 "issue_ref":"TSK-10","issue_title":"nameless and quiet","started_at":"2026-08-29T06:00:00Z","last_seen":"2026-08-29T07:00:00Z","own":false},
	 {"session_id":"s-3","email":"bob@example.com","machine":"desktop","agent":"claude-code",
	 "issue_ref":"TSK-11","issue_title":"named and quiet","started_at":"2026-08-29T06:00:00Z","last_seen":"2026-08-29T07:00:00Z","own":false}],
	 "waiting_for_pickup":[],"recent_parks":[]}`, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"team"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	if !strings.Contains(text, "TSK-9 nameless — buildbox") {
		t.Errorf("an unnamed session was dropped or mislabelled:\n%s", text)
	}
	// The gone-quiet row names the box once, as its owner, and stops there.
	quiet := teamRow(t, text, "TSK-10")
	if !strings.HasPrefix(quiet, "TSK-10 nameless and quiet — buildbox · last seen ") {
		t.Errorf("an unnamed stale session was mislabelled: %q", quiet)
	}
	if n := strings.Count(quiet, "buildbox"); n != 1 {
		t.Errorf("an unnamed stale session named its machine %d times: %q", n, quiet)
	}
	// A holder with an address still gets the machine as the trailing fact.
	named := teamRow(t, text, "TSK-11")
	if !strings.HasPrefix(named, "TSK-11 named and quiet — bob@example.com · last seen ") ||
		!strings.HasSuffix(named, " · desktop") {
		t.Errorf("a named stale session lost the machine it was on: %q", named)
	}
}

// teamRow returns the one output line mentioning ref, trimmed. The rows carry
// live ages ("1d ago"), so a test reads the parts around them rather than
// pinning a clock the command does not take.
func teamRow(t *testing.T, text, ref string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, ref+" ") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no row for %s:\n%s", ref, text)
	return ""
}
