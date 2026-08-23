// cli/auth_status_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// statusServer answers /api/auth/status with body and everything else with
// a minimal context view, which is all the two commands under test need.
func statusServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/status" {
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(`{"machine":"m","open_issues":0}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthStatusPrintsTheActorTheCredentialWritesAs(t *testing.T) {
	srv := statusServer(t, `{"authenticated":true,"required":true,"actor":"agent","scopes":["read","write"],"key_id":"k-1"}`)
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"auth", "status"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"agent", "read", "write", "k-1", srv.URL} {
		if !strings.Contains(got, want) {
			t.Fatalf("auth status output missing %q:\n%s", want, got)
		}
	}
}

// A key minted without the optional actor argument writes as the user, and
// an agent holding one pollutes the ledger under a name that is not its
// own. Saying "user" is not enough on its own — the way out has to be in
// reach of the reader who just learned it.
func TestAuthStatusNamesTheRemedyForAUserKey(t *testing.T) {
	srv := statusServer(t, `{"authenticated":true,"required":true,"actor":"user","scopes":["read"],"key_id":"k-2"}`)
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"auth", "status"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "taskr-admin key actor k-2 agent") {
		t.Fatalf("want the remedy spelled out with the key id, got:\n%s", out.String())
	}
}

func TestAuthStatusReportsBeingUnauthenticated(t *testing.T) {
	srv := statusServer(t, `{"authenticated":false,"required":true}`)
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL})
	if code := Run([]string{"auth", "status"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "not authenticated") {
		t.Fatalf("want an unauthenticated report, got:\n%s", got)
	}
	// No credential means no actor. Naming one would be a guess dressed as
	// a fact, which is the failure this whole issue is about.
	if strings.Contains(got, "user") || strings.Contains(got, "agent") {
		t.Fatalf("want no actor claimed when unauthenticated, got:\n%s", got)
	}
}

func TestAuthStatusJSON(t *testing.T) {
	srv := statusServer(t, `{"authenticated":true,"required":true,"actor":"agent","scopes":["read"],"key_id":"k-1"}`)
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"auth", "status", "--json"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	var v struct {
		Actor string `json:"actor"`
	}
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal %q: %v", out.String(), err)
	}
	if v.Actor != "agent" {
		t.Fatalf("actor = %q, want agent", v.Actor)
	}
}

// Orientation is where an agent finds out who it is before it writes
// anything. A mislabelled key noticed here costs nothing; noticed in the
// ledger afterwards, it costs a cleanup.
func TestContextReportsTheActor(t *testing.T) {
	srv := statusServer(t, `{"authenticated":true,"required":true,"actor":"agent","scopes":["read"],"key_id":"k-1"}`)
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "agent") {
		t.Fatalf("want context to name the actor, got:\n%s", out.String())
	}
}

// The actor is a nicety on top of the answer, not the answer. An instance
// that cannot report it — an older server, a transient failure — must still
// orient the caller rather than failing the command.
func TestContextSurvivesAStatusEndpointThatFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/status" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"machine":"m","open_issues":0}`))
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "open issues") {
		t.Fatalf("want the context render regardless, got:\n%s", out.String())
	}
}

// An instance older than TSK-38 authenticates fine and reports no actor at
// all. Printing "writes as" with a blank after it is worse than the silence
// it replaced: it reads as an answer. Found by running this against a live
// instance that had not been redeployed yet.
func TestAuthStatusSaysSoWhenTheInstanceWithholdsTheActor(t *testing.T) {
	srv := statusServer(t, `{"authenticated":true,"required":true}`)
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"auth", "status"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	if strings.Contains(got, "writes as") {
		t.Fatalf("want no actor line when the instance reported none, got:\n%s", got)
	}
	if !strings.Contains(got, "authenticated") {
		t.Fatalf("want the authenticated state still reported, got:\n%s", got)
	}
	// And the reason, so the reader chases the server rather than the key.
	if !strings.Contains(got, "does not report") {
		t.Fatalf("want the missing actor explained, got:\n%s", got)
	}
}

// The `plan` line is how `taskr auth status` surfaces the org's billing
// state without a separate command — the trial's end date is the one fact
// worth a glance, so it is printed rather than left for `--json`.
func TestAuthStatusPrintsThePlan(t *testing.T) {
	srv := statusServer(t, `{"authenticated":true,"required":true,"actor":"user","billing":{"status":"trial","writable":true,"trial_ends_at":"2026-09-05T12:00:00Z","seats":1}}`)
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"auth", "status"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "plan      trial, ends 2026-09-05") {
		t.Fatalf("want the plan line, got:\n%s", out.String())
	}
}
