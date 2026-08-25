// cli/completions_test.go
package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestCompletionCommandsStayInSync holds the generator's command list
// against the usage block: every first word the help advertises must be
// completable, and every completable name must be real. Three lists want
// to live here (dispatch, usage, completions); none can derive another,
// so a test is what keeps them honest.
func TestCompletionCommandsStayInSync(t *testing.T) {
	re := regexp.MustCompile(`(?m)^ {2}taskr ([a-z]+)`)
	helped := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(usage, -1) {
		helped[m[1]] = true
	}
	listed := map[string]bool{}
	for _, c := range completionCommands {
		if listed[c] {
			t.Errorf("completionCommands lists %q twice", c)
		}
		listed[c] = true
	}
	for c := range helped {
		if !listed[c] {
			t.Errorf("help text offers %q but completions would not tab-complete it", c)
		}
	}
	for c := range listed {
		if !helped[c] {
			t.Errorf("completions offer %q but the help text does not advertise it", c)
		}
	}
}

func TestCompletionsEmitPerShell(t *testing.T) {
	var out bytes.Buffer
	if err := runCompletions([]string{"bash"}, &out, io.Discard); err != nil {
		t.Fatalf("bash: %v", err)
	}
	bash := out.String()
	if !strings.Contains(bash, "complete -F _taskr_complete taskr") || !strings.Contains(bash, "__complete refs") {
		t.Errorf("bash script missing its hook:\n%s", bash)
	}

	out.Reset()
	if err := runCompletions([]string{"zsh"}, &out, io.Discard); err != nil {
		t.Fatalf("zsh: %v", err)
	}
	if !strings.HasPrefix(out.String(), "#compdef taskr") {
		t.Errorf("zsh script missing compdef header:\n%s", out.String())
	}

	out.Reset()
	if err := runCompletions([]string{"fish"}, &out, io.Discard); err != nil {
		t.Fatalf("fish: %v", err)
	}
	if !strings.Contains(out.String(), "__fish_use_subcommand") {
		t.Errorf("fish script missing its condition hook:\n%s", out.String())
	}

	out.Reset()
	if err := runCompletions([]string{"tcsh"}, &out, io.Discard); err == nil {
		t.Errorf("tcsh accepted")
	}
}

// TestCompleteRefsPrintsSortedRefs pins the wire contract with the shell
// hooks: one ref per line, sorted, nothing else on stdout.
func TestCompleteRefsPrintsSortedRefs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":[{"ref":"TSK-9"},{"ref":"TSK-2"},{"ref":"TSK-10"}]}`)
	}))
	defer srv.Close()

	var out bytes.Buffer
	c := &Client{BaseURL: srv.URL, Key: "x"}
	completeRefs(context.Background(), c, &out)
	if got := out.String(); got != "TSK-10\nTSK-2\nTSK-9\n" {
		t.Errorf("__complete refs printed %q, want sorted refs one per line", got)
	}
}

// TestCompleteRefsSilentOnDeadHost is the degrade rule: an unreachable
// host costs an empty candidate list, never an error, a warning or a hang.
func TestCompleteRefsSilentOnDeadHost(t *testing.T) {
	var out bytes.Buffer
	c := &Client{BaseURL: "http://127.0.0.1:1", Key: "x"}
	completeRefs(context.Background(), c, &out)
	if out.Len() != 0 {
		t.Errorf("dead host produced output %q, want silence", out.String())
	}
}
