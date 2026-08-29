// cli/hooks_test.go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The hook verbs are the only two commands in this CLI a human never runs,
// and they fire hundreds of times a day inside somebody's session. So what
// these tests hold is not features but restraint: silent, bounded, exit 0
// whatever happened, and never a guess in the note. TSK-201.

// contextBody is what GET /api/context answers for a machine with one active
// session on TSK-1, three steps in and one in progress.
const contextBody = `{
	"machine": "desktop",
	"active_session": {"id":"sess-1","machine":"desktop","agent":"claude-code","status":"active","started_at":"2026-08-29T10:00:00Z"},
	"active_issue": {
		"id":"i-1","ref":"TSK-1","title":"the held one","status":"in_progress",
		"step_progress": {"done":2,"total":6,"current":{"id":"s-3","position":3,"title":"add the empty-header test","status":"in_progress"}}
	},
	"open_issues": 4
}`

// A touch is one request, and it is the write — not a read to find the
// session followed by a write. A hook that fired a GET /api/context on every
// turn would spend a real query per keystroke to send a timestamp.
func TestTouchIsOneRequestAddressedByMachineAndAgent(t *testing.T) {
	t.Chdir(t.TempDir())
	var paths []string
	var body TouchWorkInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	if err := cmdTouch(context.Background(), clientFor(srv.URL), nil, &out, &errb, "desktop", "sess-abc", nil); err != nil {
		t.Fatalf("cmdTouch: %v", err)
	}

	if len(paths) != 1 || paths[0] != "POST /api/work/touch" {
		t.Fatalf("requests = %v, want exactly one POST /api/work/touch", paths)
	}
	if body.Machine != "desktop" || body.AgentSessionID != "sess-abc" {
		t.Errorf("body = %+v, want the machine and agent session id", body)
	}
	if body.SessionID != "" {
		t.Errorf("body carries session_id %q — the CLI does not know one without reading first", body.SessionID)
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("touch printed %q / %q, want silence inside a session", out.String(), errb.String())
	}
}

// The rule that makes a hook safe to plant on every turn: taskr being down
// is invisible from inside the session. No output, no error, exit 0.
func TestTouchIsSilentAndSucceedsWhenTheAPIIsUnreachable(t *testing.T) {
	t.Chdir(t.TempDir())
	// A server that is closed before the call: connection refused, the
	// shape an outage or a stopped port-forward actually takes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	var out, errb bytes.Buffer
	if err := cmdTouch(context.Background(), clientFor(url), nil, &out, &errb, "desktop", "sess-1", nil); err != nil {
		t.Fatalf("cmdTouch against a dead API: %v — a hook must never fail", err)
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("touch printed %q / %q against a dead API, want silence", out.String(), errb.String())
	}

	// The same through Run, because the exit code is what the harness sees.
	env := envAt(map[string]string{"TASKR_API": url, "TASKR_KEY": "x"})
	if code := Run([]string{"touch"}, &out, &errb, env); code != 0 {
		t.Errorf("taskr touch exited %d against a dead API, want 0", code)
	}
}

// The hook shell is not guaranteed to carry CLAUDE_CODE_SESSION_ID, so the
// payload the harness pipes in is the authority. Getting this wrong is not a
// missing touch: it is a touch that keeps the WRONG session alive, or opens
// a second one per turn under a parent pid.
func TestHookPayloadNamesTheSession(t *testing.T) {
	cases := []struct {
		name string
		in   io.Reader
		want string
	}{
		{"a harness event", strings.NewReader(`{"session_id":"02fccd07","hook_event_name":"Stop"}`), "02fccd07"},
		{"no stdin at all", nil, ""},
		{"empty stdin", strings.NewReader(""), ""},
		{"not JSON", strings.NewReader("Stop\n"), ""},
		{"JSON without the field", strings.NewReader(`{"hook_event_name":"Stop"}`), ""},
		{"a blank field", strings.NewReader(`{"session_id":"  "}`), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hookSessionID(c.in); got != c.want {
				t.Errorf("hookSessionID = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTouchPrefersTheHookPayloadOverTheEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	var body TouchWorkInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"session_id":"from-the-hook"}`)
	if err := cmdTouch(context.Background(), clientFor(srv.URL), nil, &out, &errb, "desktop", "ppid-4242", stdin); err != nil {
		t.Fatalf("cmdTouch: %v", err)
	}
	if body.AgentSessionID != "from-the-hook" {
		t.Errorf("agent_session_id = %q, want from-the-hook — the payload outranks a ppid fallback", body.AgentSessionID)
	}
}

// The whole SessionEnd path, from the harness's stdin to the park on the
// wire: the session is parked, marked auto, and carries a note assembled from
// facts rather than a sentence that sounds like a person wrote it.
func TestAutoParkParksTheSessionWithAMechanicalNote(t *testing.T) {
	t.Chdir(t.TempDir())
	var parked ParkWorkInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/context":
			fmt.Fprint(w, contextBody)
		case "/api/work/park":
			_ = json.NewDecoder(r.Body).Decode(&parked)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	env := envAt(map[string]string{
		"TASKR_BRANCH": "fix/auth-cookie",
		"TASKR_HEAD":   "9f3c2ab1122334455667788990011223344556677",
		"TASKR_DIRTY":  "internal/api/auth.go\ninternal/api/auth_test.go",
	})
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"session_id":"02fccd07","hook_event_name":"SessionEnd"}`)
	if err := autoPark(context.Background(), clientFor(srv.URL), &out, &errb, "desktop", "ignored", env, stdin, false); err != nil {
		t.Fatalf("autoPark: %v", err)
	}

	if parked.SessionID != "sess-1" {
		t.Fatalf("parked session = %q, want sess-1", parked.SessionID)
	}
	if !parked.Auto {
		t.Error("auto = false — a park nobody chose has to say so on the ledger")
	}
	if parked.Reason != "interrupted" {
		t.Errorf("reason = %q, want interrupted — nobody said the work was done", parked.Reason)
	}
	for _, want := range []string{
		"auto-park (session ended)",
		"fix/auth-cookie @ 9f3c2ab11223",
		"dirty: internal/api/auth.go, internal/api/auth_test.go",
		`step 3/6 "add the empty-header test" in progress`,
	} {
		if !strings.Contains(parked.ResumeNote, want) {
			t.Errorf("note is missing %q:\n%s", want, parked.ResumeNote)
		}
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("auto-park printed %q / %q, want silence", out.String(), errb.String())
	}
}

// Most SessionEnd events this will ever see are windows closing on a machine
// where nobody ran `taskr start`. That is not a failure and must not become
// one — nor may it park somebody else's session out from under them.
func TestAutoParkDoesNothingWithoutAnActiveSession(t *testing.T) {
	t.Chdir(t.TempDir())
	parkCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/context":
			fmt.Fprint(w, `{"machine":"desktop","open_issues":0}`)
		case "/api/work/park":
			parkCalled = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	if err := autoPark(context.Background(), clientFor(srv.URL), &out, &errb, "desktop", "sess-1", envAt(map[string]string{}), nil, false); err != nil {
		t.Fatalf("autoPark: %v", err)
	}
	if parkCalled {
		t.Error("auto-park parked something with no active session")
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("printed %q / %q, want silence", out.String(), errb.String())
	}
}

func TestAutoParkIsSilentWhenTheAPIIsUnreachable(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	var out, errb bytes.Buffer
	if err := autoPark(context.Background(), clientFor(url), &out, &errb, "desktop", "sess-1", envAt(map[string]string{}), nil, false); err != nil {
		t.Fatalf("autoPark against a dead API: %v — a hook must never fail", err)
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("printed %q / %q against a dead API, want silence", out.String(), errb.String())
	}
}

// The note is the only thing an auto-park writes, and every clause in it has
// to be a fact the CLI read rather than one it inferred. A checkout it cannot
// read yields a shorter note, never an invented one.
func TestAutoParkNoteOmitsWhatItCannotRead(t *testing.T) {
	note := autoParkNote(envAt(map[string]string{}), nil)
	if strings.Contains(note, ";") {
		t.Errorf("note invented clauses from an empty environment:\n%s", note)
	}
	if !strings.Contains(note, "auto-park (session ended)") || !strings.Contains(note, "No human note") {
		t.Errorf("note = %q, want the prefix and the warning even with nothing to say", note)
	}

	detached := autoParkNote(envAt(map[string]string{"TASKR_HEAD": "abc123def456789"}), nil)
	if !strings.Contains(detached, "(detached) @ abc123def456") {
		t.Errorf("note = %q, want a detached head named as such", detached)
	}
}

// park -m and park --auto are two different commands wearing one name, and
// the flag that composes a note cannot also accept one.
func TestParkAutoRefusesAHandWrittenNote(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": "http://127.0.0.1:1", "TASKR_KEY": "x"})
	if code := Run([]string{"park", "--auto", "-m", "stopped for lunch"}, &out, &errb, env); code == 0 {
		t.Error("park --auto -m succeeded, want a refusal — the note is composed, not accepted")
	}
}

// clientFor is the client the hook verbs are handed, pointed at a test
// server. The hook path never resolves a target from config: these tests
// drive the command functions directly, which is also the only way to feed
// them a stdin that is not the process's own.
func clientFor(url string) *Client { return &Client{BaseURL: url, Key: "x"} }

// The flag exists so the reader is warned BEFORE the note, not after it: an
// auto-park's note is mechanically true and says nothing about why the work
// stopped, and a reader who takes it for a human handoff trusts a sentence
// nobody meant.
func TestStartWarnsAboveAnAutoParkedNote(t *testing.T) {
	packet := ResumePacket{
		Issue: IssueView{Ref: "TSK-1", Title: "the auto-parked one", Status: "parked"},
		LastPark: &ParkView{
			Reason:     "interrupted",
			ParkedAt:   "2026-08-29T10:00:00Z",
			ResumeNote: "auto-park (session ended): fix/auth @ 9f3c2ab11223.",
			Auto:       true,
		},
	}
	out := RenderResumePacket(packet)
	warning := strings.Index(out, "Nobody wrote this note")
	note := strings.Index(out, "What to do next")
	if warning == -1 {
		t.Fatalf("an auto-park renders no warning:\n%s", out)
	}
	if note != -1 && warning > note {
		t.Errorf("the warning prints after the note it is about:\n%s", out)
	}

	packet.LastPark.Auto = false
	if strings.Contains(RenderResumePacket(packet), "Nobody wrote this note") {
		t.Error("a park a person made is being reported as automatic")
	}
}
