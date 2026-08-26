// cli/triage_cmd_test.go
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

// triageQueueServer answers GET /api/triage with body, recording the query
// it was asked with, and fails the test on any other path.
func triageQueueServer(t *testing.T, body string, seen *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The single-ref flow consults the duplicate gate (TSK-167); an
		// empty answer keeps these tests about the queue itself.
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/neighbors") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "null")
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/triage" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		*seen = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

// TestTriageBareListsTheQueue is the verb TSK-158 adds: `taskr triage` with
// nothing after it asks the server what needs a verdict, scoped to the repo
// the caller is standing in, and says why each row is there in words.
func TestTriageBareListsTheQueue(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[
		{"issue_id":"i-1","issue_ref":"TSK-1","title":"never looked at","reason":"new"},
		{"issue_id":"i-2","issue_ref":"TSK-2","title":"drifted","reason":"rot","snapshot_sha":"abcdef1234567890","latest_sha":"0123456789abcdef","triaged_at":"2026-08-01T10:00:00Z"},
		{"issue_id":"i-3","issue_ref":"TSK-3","title":"old verdict","reason":"expired","triaged_at":"2026-08-01T10:00:00Z"}
	]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x", "TASKR_REMOTE": "https://github.com/you/app.git"})
	if code := Run([]string{"triage"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if seen.Get("remote_url") != "https://github.com/you/app.git" {
		t.Errorf("want the queue scoped by remote_url, got query %v", seen)
	}
	if seen.Has("ref") || seen.Has("all") {
		t.Errorf("bare triage must not send ref or all, got %v", seen)
	}
	got := out.String()
	for _, want := range []string{
		"REF", "REASON", "WHY",
		"TSK-1", "never triaged",
		"TSK-2", "rot", "abcdef123456 -> 0123456789ab",
		"TSK-3", "expired", "triaged 2026-08-01",
		"taskr triage <ref> <verdict>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in output, got:\n%s", want, got)
		}
	}
}

// TestTriageAllWidens keeps the wider view one flag away, like next and ls.
func TestTriageAllWidens(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"triage", "--all"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if seen.Get("all") != "1" {
		t.Errorf("want all=1 on the wire, got %v", seen)
	}
	if !strings.Contains(out.String(), "Nothing needs a verdict in any project") {
		t.Errorf("want the empty-queue line, got:\n%s", out.String())
	}
}

// TestTriageEmptyQueueSaysSo: an empty table reads as nothing there, which
// is true, but the reader still needs to know --all exists.
func TestTriageEmptyQueueSaysSo(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"triage"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Nothing needs a verdict in this project") || !strings.Contains(out.String(), "--all") {
		t.Errorf("want the empty-queue line naming --all, got:\n%s", out.String())
	}
}

// TestTriageRefAsksAboutOneIssue: a ref with no verdict is a question, not
// a usage error. The server's ?ref= filter answers it; an empty answer is
// the good news, said in words — and said for both things it can mean,
// since the queue walks open issues only and a closed ref answers empty
// as well.
func TestTriageRefAsksAboutOneIssue(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"triage", "TSK-7"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if seen.Get("ref") != "TSK-7" {
		t.Errorf("want ref=TSK-7 on the wire, got %v", seen)
	}
	if !strings.Contains(out.String(), "TSK-7 needs no verdict right now") || !strings.Contains(out.String(), "closed") {
		t.Errorf("want the fresh-verdict line, got:\n%s", out.String())
	}
}

// TestTriageRefThatNeedsOneShowsWhy: the single-issue form prints the same
// row the queue would, so the reason and its evidence are on screen.
func TestTriageRefThatNeedsOneShowsWhy(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[{"issue_id":"i-2","issue_ref":"TSK-2","title":"drifted","reason":"rot","snapshot_sha":"abcdef1234567890","latest_sha":"0123456789abcdef"}]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"triage", "TSK-2"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "the branch moved under it: abcdef123456 -> 0123456789ab") {
		t.Errorf("want the rot reason with both SHAs, got:\n%s", out.String())
	}
}

// TestTriageJSONIsTheWireRows: --json hands agents the server's rows
// untouched — reason tokens, SHAs and timestamps — and an empty queue is
// an empty array, never null.
func TestTriageJSONIsTheWireRows(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"triage", "--json"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("want an empty JSON array, got:\n%s", out.String())
	}
}

// TestTriageVerdictStillPosts pins the write form: a ref and a verdict
// record one, exactly as before the list forms existed.
func TestTriageVerdictStillPosts(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		var b bytes.Buffer
		b.ReadFrom(r.Body)
		gotBody = b.String()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"triage", "TSK-1", "already_fixed", "-e", "auth.go:104"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if gotPath != "POST /api/triage/TSK-1" {
		t.Errorf("want POST /api/triage/TSK-1, got %s", gotPath)
	}
	if !strings.Contains(gotBody, `"already_fixed"`) || !strings.Contains(gotBody, "auth.go:104") {
		t.Errorf("want verdict and evidence in the body, got %s", gotBody)
	}
	if !strings.Contains(out.String(), `Recorded verdict "already_fixed" for TSK-1`) {
		t.Errorf("want the recorded line, got:\n%s", out.String())
	}
}

// TestTriageEvidenceWithoutVerdictIsAUsageError: -e on the list form is a
// forgotten verdict, and saying so beats silently listing the queue.
func TestTriageEvidenceWithoutVerdictIsAUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": "http://127.0.0.1:1", "TASKR_KEY": "x"})
	if code := Run([]string{"triage", "TSK-1", "-e", "auth.go:104"}, &out, &errb, env); code == 0 {
		t.Fatalf("want a usage error, got exit 0 with:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "-e and -d go with a verdict") {
		t.Errorf("want the usage line, got stderr:\n%s", errb.String())
	}
}

// TestTriageListFormsAppearInTheHelpText: a verb nobody can find is no
// verb. The whole failure TSK-158 fixes is an agent reaching for `ls`
// because --help offered nothing that listed the queue.
func TestTriageListFormsAppearInTheHelpText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--help"}, &stdout, &stderr, envAt(map[string]string{})); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{"taskr triage [--all]", "taskr triage <ref>  ", "taskr triage <ref> <verdict>"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help text does not mention %q:\n%s", want, stdout.String())
		}
	}
}

// TestTriageRefShowsDuplicateCandidates pins the triage assist (TSK-167):
// examining one issue lists semantic twins with scores and the exact
// command that records the verdict, so `duplicate -d` no longer requires
// prior knowledge that a twin exists.
func TestTriageRefShowsDuplicateCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/triage":
			fmt.Fprint(w, `[{"issue_id":"i-1","issue_ref":"TSK-7","title":"can't login after reset","reason":"new"}]`)
		case strings.HasSuffix(r.URL.Path, "/neighbors"):
			fmt.Fprint(w, `[{"id":"i-2","ref":"TSK-4","title":"auth 401 on reset flow","status":"open","score":0.93}]`)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x", "TASKR_REMOTE": "https://github.com/you/app.git"})
	if code := Run([]string{"triage", "TSK-7"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	text := out.String()
	for _, want := range []string{"Similar open issues:", "0.93", "TSK-4", "duplicate -d <ref>"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

// TestTriageBareShowsTwins is the scan-time duplicate net (TSK-178): a row
// the server paired with a twin carries it in a TWIN column, score then
// ref like every other suggestion, and the footer names the verdict that
// collapses the pair. A row without one leaves the cell empty.
func TestTriageBareShowsTwins(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[
		{"issue_id":"i-1","issue_ref":"TSK-176","title":"resetting my password then logging in fails","reason":"new",
		 "twin":{"id":"i-2","ref":"TSK-177","title":"login 401 after completing password reset","status":"open","score":0.91}},
		{"issue_id":"i-3","issue_ref":"TSK-9","title":"export to csv times out","reason":"new"}
	]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x", "TASKR_REMOTE": "https://github.com/you/app.git"})
	if code := Run([]string{"triage"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"TWIN", "0.91 TSK-177", "duplicate -d <twin>"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in output, got:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "TSK-9") && strings.Contains(line, "TSK-177") {
			t.Errorf("the twinless row borrowed a twin:\n%s", got)
		}
	}
}

// TestTriageBareHidesTheTwinColumnWhenNoneExist keeps the feature-off table
// exactly what it was: no column, no footer, nothing to explain.
func TestTriageBareHidesTheTwinColumnWhenNoneExist(t *testing.T) {
	var seen url.Values
	srv := triageQueueServer(t, `[{"issue_id":"i-1","issue_ref":"TSK-1","title":"never looked at","reason":"new"}]`, &seen)
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x", "TASKR_REMOTE": "https://github.com/you/app.git"})
	if code := Run([]string{"triage"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if got := out.String(); strings.Contains(got, "TWIN") {
		t.Errorf("TWIN column rendered with no twins:\n%s", got)
	}
}
