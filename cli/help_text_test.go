// cli/help_text_test.go
package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/thomas-cabral/taskr-cli/cli"
)

// getenv stands in for the private repo's getenvFor(nil, nil): this test
// never makes a request, so TASKR_API and TASKR_KEY stay unset, but
// XDG_CONFIG_HOME still has to point somewhere that cannot exist so a stray
// hosts.json on the machine running this test can't leak a key into it.
func getenv(k string) string {
	if k == "XDG_CONFIG_HOME" {
		return "/nonexistent-taskr-test-config"
	}
	return ""
}

// TestCloseAppearsInTheHelpText pins the affordance is discoverable. A verb
// nobody can find is the same as no verb: the whole failure this task fixes
// is an agent reaching for a hand-rolled PATCH because nothing in `taskr
// --help` offered anything better.
func TestCloseAppearsInTheHelpText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"--help"}, &stdout, &stderr, getenv); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "taskr close") {
		t.Errorf("help text does not mention `taskr close`:\n%s", stdout.String())
	}
}
