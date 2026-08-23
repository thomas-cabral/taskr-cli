// cli/locator_test.go
package cli_test

import (
	"testing"

	"github.com/thomas-cabral/taskr-cli/cli"
)

// TestLocatorDerivesSubpathFromTaskrRoot covers the normal case: the agent
// exported the repo root, and the CLI works out where inside it we are.
func TestLocatorDerivesSubpathFromTaskrRoot(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{
			"TASKR_REMOTE": "git@github.com:you/mono.git",
			"TASKR_ROOT":   "/home/me/projects/mono",
		}[k]
	}

	got := cli.LocatorFrom(getenv, "/home/me/projects/mono/apps/api/internal")
	if got.RemoteURL != "git@github.com:you/mono.git" {
		t.Errorf("RemoteURL = %q", got.RemoteURL)
	}
	if got.Subpath != "apps/api/internal" {
		t.Errorf("Subpath = %q, want apps/api/internal", got.Subpath)
	}
}

// TestLocatorAtTheRepoRootHasNoSubpath — the root is the empty subpath, not "."
func TestLocatorAtTheRepoRootHasNoSubpath(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{
			"TASKR_REMOTE": "git@github.com:you/mono.git",
			"TASKR_ROOT":   "/home/me/projects/mono",
		}[k]
	}
	if got := cli.LocatorFrom(getenv, "/home/me/projects/mono"); got.Subpath != "" {
		t.Errorf("Subpath = %q, want empty at the repo root", got.Subpath)
	}
}

// TestLocatorWithoutRootSendsOnlyTheRemote keeps an agent that exported
// TASKR_REMOTE but not TASKR_ROOT working: a single-project repo resolves
// from the remote alone.
func TestLocatorWithoutRootSendsOnlyTheRemote(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{"TASKR_REMOTE": "git@github.com:you/solo.git"}[k]
	}
	got := cli.LocatorFrom(getenv, "/home/me/projects/solo/internal")
	if got.RemoteURL == "" {
		t.Error("RemoteURL is empty")
	}
	if got.Subpath != "" {
		t.Errorf("Subpath = %q, want empty without TASKR_ROOT", got.Subpath)
	}
}

// TestLocatorOutsideTheRootSendsNoSubpath guards a CWD that is not under
// TASKR_ROOT — a stale export must not invent a ../.. subpath.
func TestLocatorOutsideTheRootSendsNoSubpath(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{
			"TASKR_REMOTE": "git@github.com:you/mono.git",
			"TASKR_ROOT":   "/home/me/projects/mono",
		}[k]
	}
	if got := cli.LocatorFrom(getenv, "/tmp/elsewhere"); got.Subpath != "" {
		t.Errorf("Subpath = %q, want empty when CWD is outside the root", got.Subpath)
	}
}
