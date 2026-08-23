// cli/version_cmd_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withStamp swaps the provenance this binary reports for the duration of a
// test. A `go test` binary carries no VCS stamp of its own — the whole
// mechanism is invisible from inside the test suite — so the only way to
// drive the stale and dirty paths end to end is to say what the stamp is.
func withStamp(t *testing.T, s buildStamp) {
	t.Helper()
	orig := readBuildStamp
	readBuildStamp = func() buildStamp { return s }
	t.Cleanup(func() { readBuildStamp = orig })
}

// envAt builds a getenv backed by a map, with the stray-credential
// precautions the rest of this package's tests take.
func envAt(m map[string]string) func(string) string {
	if _, ok := m["TASKR_KEY"]; !ok {
		m["TASKR_KEY"] = ""
	}
	if _, ok := m["XDG_CONFIG_HOME"]; !ok {
		m["XDG_CONFIG_HOME"] = "/nonexistent-taskr-test-config"
	}
	return func(k string) string { return m[k] }
}

// taskrCheckout is a directory that reads as taskr's own source tree: a
// go.mod declaring the module this binary was built from.
func taskrCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := writeFile(dir+"/go.mod", "module "+mod+"\n\ngo 1.26\n"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVersionRunsWithoutAServer(t *testing.T) {
	withStamp(t, buildStamp{Module: mod, Revision: built, Time: "2026-08-19T19:36:06Z", GoVersion: "go1.26.5"})
	var out, errb bytes.Buffer
	// No TASKR_API, no key, nothing reachable. A binary you suspect of
	// being stale is exactly the one that may not reach an API at all, so
	// the answer must not depend on one.
	if code := Run([]string{"version"}, &out, &errb, envAt(map[string]string{})); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{mod, built[:12], "2026-08-19T19:36:06Z", "go1.26.5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("version output missing %q:\n%s", want, got)
		}
	}
}

func TestVersionReportsStalenessAgainstTheCheckout(t *testing.T) {
	withStamp(t, buildStamp{Module: mod, Revision: built})
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_ROOT": taskrCheckout(t), "TASKR_HEAD": head})
	if code := Run([]string{"version"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), head[:12]) {
		t.Fatalf("want the checkout's revision reported, got:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "old code") {
		t.Fatalf("want the staleness warning on stderr, got: %q", errb.String())
	}
}

func TestVersionJSONCarriesTheStamp(t *testing.T) {
	withStamp(t, buildStamp{Module: mod, Revision: built, Dirty: true, GoVersion: "go1.26.5"})
	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_ROOT": taskrCheckout(t), "TASKR_HEAD": built})
	if code := Run([]string{"version", "--json"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	var v struct {
		Module   string `json:"module"`
		Revision string `json:"revision"`
		Dirty    bool   `json:"dirty"`
		Head     string `json:"checkout_revision"`
		Stale    bool   `json:"stale"`
	}
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal %q: %v", out.String(), err)
	}
	if v.Module != mod || v.Revision != built || !v.Dirty || v.Head != built {
		t.Fatalf("stamp round-trip wrong: %+v", v)
	}
	// A dirty build is stale in the only sense that matters: the revision
	// does not identify the code that is running.
	if !v.Stale {
		t.Fatalf("want a dirty build reported as stale: %+v", v)
	}
}

// The orientation command is where an agent starts a session, and so the
// one place a stale binary has to announce itself unasked.
func TestContextWarnsAboutAStaleBinary(t *testing.T) {
	withStamp(t, buildStamp{Module: mod, Revision: built})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"machine":"m","open_issues":0}`))
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_ROOT": taskrCheckout(t), "TASKR_HEAD": head})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), built[:12]) || !strings.Contains(errb.String(), head[:12]) {
		t.Fatalf("want context to warn with both revisions, got: %q", errb.String())
	}
	// The warning is a side note, not the answer: it must not contaminate
	// stdout, which --json callers parse.
	if !strings.Contains(out.String(), "open issues") {
		t.Fatalf("want the context render still on stdout, got:\n%s", out.String())
	}
}

func TestContextIsSilentWhenTheBinaryMatches(t *testing.T) {
	withStamp(t, buildStamp{Module: mod, Revision: built})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"machine":"m","open_issues":0}`))
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_ROOT": taskrCheckout(t), "TASKR_HEAD": built})
	if code := Run([]string{"context"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if errb.String() != "" {
		t.Fatalf("want no warning when the binary is current, got: %q", errb.String())
	}
}
