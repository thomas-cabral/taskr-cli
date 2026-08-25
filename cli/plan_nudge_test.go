package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TSK-139. An agent can write a whole plan with `step add` and never move a
// step again, and until these the only thing that noticed was a human
// asking why `step ls` read 0/11 after three tasks had shipped. The server
// reports the condition on the context view; these pin that the CLI says
// it where it can still be acted on.

const untouchedContext = `{"machine":"laptop",
	"active_session":{"id":"sess-1","machine":"laptop","issue_id":"i-66"},
	"active_issue":{"id":"i-66","ref":"TSK-66","title":"device flow","status":"in_progress"},
	"untouched_plan":{"issue_ref":"TSK-66","open":11,"since":"2026-08-25T10:12:00Z"}}`

const movedContext = `{"machine":"laptop",
	"active_session":{"id":"sess-1","machine":"laptop","issue_id":"i-66"},
	"active_issue":{"id":"i-66","ref":"TSK-66","title":"device flow","status":"in_progress"}}`

func planServer(t *testing.T, contextJSON string, parked *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/context":
			fmt.Fprint(w, contextJSON)
		case "/api/work/park":
			if parked != nil {
				*parked = true
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/auth/status":
			fmt.Fprint(w, `{"authenticated":true,"required":true,"actor":"agent"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

// TestParkWarnsBeforeParkingWhenThePlanNeverMoved pins both the warning and
// its position: it has to come out BEFORE the park, because the session
// that could still mark those steps is gone the moment the park lands, and
// a warning printed under "Parked" reads as history.
func TestParkWarnsBeforeParkingWhenThePlanNeverMoved(t *testing.T) {
	var parked bool
	srv := planServer(t, untouchedContext, &parked)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"park", "-m", "next: wire the sweeper", "-r", "done_for_now"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	for _, want := range []string{"11 step(s)", "TSK-66", "taskr step done TSK-66", "Parked TSK-66"} {
		if !strings.Contains(text, want) {
			t.Errorf("park output does not mention %q:\n%s", want, text)
		}
	}
	if warn, done := strings.Index(text, "Plan untouched"), strings.Index(text, "Parked TSK-66"); warn < 0 || done < 0 || warn > done {
		t.Errorf("the warning must precede the park line:\n%s", text)
	}
	if !parked {
		t.Error("the warning must not refuse the park — a plan is not a promise")
	}
}

// TestParkIsQuietWhenThePlanMoved keeps the line honest: a session that kept
// its plan, or an issue with no plan at all, gets no warning.
func TestParkIsQuietWhenThePlanMoved(t *testing.T) {
	srv := planServer(t, movedContext, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"park", "-m", "next: wire the sweeper"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "Plan untouched") {
		t.Errorf("park warned about a plan that moved:\n%s", out.String())
	}
}

// TestParkJSONCarriesTheUntouchedPlan: an agent reading --json is told the
// same thing a person reading prose is, on the record it parses.
func TestParkJSONCarriesTheUntouchedPlan(t *testing.T) {
	srv := planServer(t, untouchedContext, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"park", "-m", "next", "--json"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"untouched_plan"`) || !strings.Contains(out.String(), `"TSK-66"`) {
		t.Errorf("park --json does not carry the plan warning:\n%s", out.String())
	}
}

// TestContextRendersAnUntouchedPlan: orientation is the other moment the
// warning can still be acted on.
func TestContextRendersAnUntouchedPlan(t *testing.T) {
	srv := planServer(t, untouchedContext, nil)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, want := range []string{"Plan untouched", "11 step(s)", "taskr step done TSK-66"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("context does not say %q:\n%s", want, out.String())
		}
	}
}

// TestCloseNamesNeverStartedSteps: the abandoned list already says what the
// plan did not reach; this line says how many of those were never even
// begun, which wants a different answer.
func TestCloseNamesNeverStartedSteps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/issues/TSK-66":
			fmt.Fprint(w, `{"id":"i-66","ref":"TSK-66","never_started":2,"abandoned_steps":[
				{"id":"s-2","position":2,"title":"wire the sweeper","status":"abandoned"},
				{"id":"s-3","position":3,"title":"revise the spec","status":"abandoned"}]}`)
		case "/api/context":
			fmt.Fprint(w, `{"machine":"laptop"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"close", "TSK-66", "-r", "shipped"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "2 of them were never started") {
		t.Errorf("close does not say how many steps were never started:\n%s", out.String())
	}
}
