// cli/adhoc_cmd_test.go
package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// adhocServer answers the two paths an offload touches and captures the
// offload body. offloadJSON is what /api/offload replies with, so one test
// can pin a routed answer and another an ad-hoc one.
func adhocServer(t *testing.T, gotBody *string, offloadJSON string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/context":
			fmt.Fprint(w, `{"machine":"laptop","active_session":{"id":"sess-1","machine":"laptop"}}`)
		case "/api/offload":
			b, _ := io.ReadAll(r.Body)
			*gotBody = string(b)
			fmt.Fprint(w, offloadJSON)
		case "/api/issues":
			b, _ := io.ReadAll(r.Body)
			*gotBody = string(b)
			fmt.Fprint(w, `{"id":"id-1","ref":"INB-3"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runCLI(args []string, api string) (string, string, int) {
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": api, "TASKR_KEY": "x", "TASKR_SESSION": "sess-1"})
	code := Run(args, &out, &errb, env)
	return out.String(), errb.String(), code
}

// TestNewAdHocSendsTheFlagAndOmitsItOtherwise pins the opt-in: --adhoc is
// the only thing that puts "adhoc":true on the wire, because on the server
// it is the difference between "this belongs to no project" and a write
// that lost its locator — and only the first is safe to file.
func TestNewAdHocSendsTheFlagAndOmitsItOtherwise(t *testing.T) {
	var gotBody string
	srv := adhocServer(t, &gotBody, "")

	out, errb, code := runCLI([]string{"new", "read the queue paper", "--adhoc"}, srv.URL)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	if !strings.Contains(gotBody, `"adhoc":true`) {
		t.Errorf("body carries no adhoc flag: %s", gotBody)
	}
	if strings.Contains(gotBody, `"project"`) {
		t.Errorf("adhoc write named a project: %s", gotBody)
	}
	if !strings.Contains(out, "Created INB-3") {
		t.Errorf("stdout wrong:\n%s", out)
	}

	gotBody = ""
	if _, errb, code := runCLI([]string{"new", "read the queue paper"}, srv.URL); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	if strings.Contains(gotBody, `"adhoc"`) {
		t.Errorf("a plain new sent adhoc: %s", gotBody)
	}
}

// TestOffloadAdHocSendsTheFlag — an offload reaches the inbox on its own
// when nothing resolves, so the flag exists for the other case: a stray
// thought had while standing in a repo that would have resolved fine.
func TestOffloadAdHocSendsTheFlag(t *testing.T) {
	var gotBody string
	srv := adhocServer(t, &gotBody, `{"issue":{"id":"id-2","ref":"INB-4"},"project":"inbox","adhoc":true}`)

	out, errb, code := runCLI([]string{"offload", "reread the CAP paper", "-m", "not this repo's work", "--adhoc"}, srv.URL)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	if !strings.Contains(gotBody, `"adhoc":true`) {
		t.Errorf("body carries no adhoc flag: %s", gotBody)
	}
	if !strings.Contains(out, "Offloaded INB-4") {
		t.Errorf("stdout wrong:\n%s", out)
	}
	if !strings.Contains(out, "Filed ad-hoc, in the inbox.") {
		t.Errorf("stdout does not say where it landed:\n%s", out)
	}
	// A caller who asked for the inbox does not need to be told to go
	// register the repo they are standing in.
	if strings.Contains(out, "project attach") {
		t.Errorf("stdout hectors a caller who asked for the inbox:\n%s", out)
	}
}

// TestOffloadNamesTheProjectItChose is the reporting half of TSK-59: the
// misroutes survived days because nothing the caller saw named the backlog
// that took the finding.
func TestOffloadNamesTheProjectItChose(t *testing.T) {
	var gotBody string
	srv := adhocServer(t, &gotBody, `{"issue":{"id":"id-2","ref":"TSK-9"},"project":"taskr"}`)

	out, errb, code := runCLI([]string{"offload", "auth drops the cookie", "-m", "internal/api/auth.go:104"}, srv.URL)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	if !strings.Contains(out, "Filed in taskr.") {
		t.Errorf("stdout does not name the project:\n%s", out)
	}
}

// TestOffloadUnaskedInboxSaysWhy — an offload that lands in the inbox
// without being asked to is evidence the repo is not registered, and the
// fix is to register it rather than to keep filing ad-hoc.
func TestOffloadUnaskedInboxSaysWhy(t *testing.T) {
	var gotBody string
	srv := adhocServer(t, &gotBody, `{"issue":{"id":"id-2","ref":"INB-5"},"project":"inbox","adhoc":true}`)

	out, errb, code := runCLI([]string{"offload", "auth drops the cookie", "-m", "internal/api/auth.go:104"}, srv.URL)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	if !strings.Contains(out, "nothing here resolves to a project") {
		t.Errorf("stdout does not say why it went to the inbox:\n%s", out)
	}
	if !strings.Contains(out, "taskr project attach") {
		t.Errorf("stdout does not name the fix:\n%s", out)
	}
}

// TestOffloadStaysQuietWhenTheServerNamesNothing pins the degradation: an
// older server sends no project, and inventing one would be the very lie
// this line exists to prevent.
func TestOffloadStaysQuietWhenTheServerNamesNothing(t *testing.T) {
	var gotBody string
	srv := adhocServer(t, &gotBody, `{"issue":{"id":"id-2","ref":"TSK-9"}}`)

	out, errb, code := runCLI([]string{"offload", "auth drops the cookie", "-m", "internal/api/auth.go:104"}, srv.URL)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb)
	}
	if strings.Contains(out, "Filed") {
		t.Errorf("stdout claims a destination the server never named:\n%s", out)
	}
}

// TestProjectAndAdHocContradictEachOther pins the local refusal, before any
// wire traffic: the server would keep the slug and drop --adhoc, and a
// silently dropped routing flag is the failure this whole ticket is about.
func TestProjectAndAdHocContradictEachOther(t *testing.T) {
	var gotBody string
	srv := adhocServer(t, &gotBody, `{"issue":{"id":"id-2","ref":"TSK-9"}}`)

	cases := [][]string{
		{"new", "a title", "--project", "taskr", "--adhoc"},
		{"offload", "a title", "-m", "a brief", "--project", "taskr", "--adhoc"},
	}
	for _, args := range cases {
		out, errb, code := runCLI(args, srv.URL)
		if code == 0 {
			t.Errorf("%v was accepted, stdout: %s", args, out)
		}
		if !strings.Contains(errb, "contradict each other") {
			t.Errorf("%v: stderr does not explain the contradiction: %s", args, errb)
		}
		if gotBody != "" {
			t.Errorf("%v reached the server: %s", args, gotBody)
		}
	}
}
