// cli/enforce_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runEnforce runs `taskr skill enforce` against a fake home and hands back
// what the user would see.
func runEnforce(t *testing.T, env func(string) string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append([]string{"skill", "enforce"}, args...), &stdout, &stderr, env)
	return stdout.String(), stderr.String(), code
}

// enforcedPaths are the global shim locations under a given home.
func enforcedPaths(home string) (claude, codex, opencode string) {
	return filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "AGENTS.md"),
		filepath.Join(home, ".config", "opencode", "AGENTS.md")
}

// The contract: one command, and every harness that can be reached from a
// home directory tells its agent to run taskr at session start. Cursor is
// per-repository and is covered separately.
func TestEnforceWritesTheGlobalShims(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir()) // not a repository: the cursor rule must be skipped

	stdout, stderr, code := runEnforce(t, homeEnv(home))
	if code != 0 {
		t.Fatalf("enforce exited %d, stderr: %s", code, stderr)
	}

	claude, codex, opencode := enforcedPaths(home)

	raw, err := os.ReadFile(claude)
	if err != nil {
		t.Fatalf("want claude settings written: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("claude settings are not valid JSON after enforce: %v", err)
	}
	if !strings.Contains(string(raw), "taskr skill nudge") {
		t.Fatalf("claude settings carry no nudge hook:\n%s", raw)
	}

	for _, p := range []string{codex, opencode} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("want %s written: %v", p, err)
		}
		text := string(got)
		if !strings.Contains(text, enforceBegin) || !strings.Contains(text, enforceEnd) {
			t.Fatalf("%s has no marked taskr block:\n%s", p, text)
		}
		if !strings.Contains(text, "taskr context") {
			t.Fatalf("%s block does not tell the agent to orient:\n%s", p, text)
		}
	}

	if !strings.Contains(stdout, "skipped") {
		t.Fatalf("outside a repository the cursor rule should report skipped, got:\n%s", stdout)
	}
}

// opencode reads its config root from XDG_CONFIG_HOME when it is set; the
// shim has to land where opencode will actually look.
func TestEnforceHonoursXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Chdir(t.TempDir())
	env := func(k string) string {
		switch k {
		case "HOME":
			return home
		case "XDG_CONFIG_HOME":
			return xdg
		}
		return ""
	}

	if _, stderr, code := runEnforce(t, env); code != 0 {
		t.Fatalf("enforce exited %d, stderr: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(xdg, "opencode", "AGENTS.md")); err != nil {
		t.Fatalf("want the opencode shim under XDG_CONFIG_HOME: %v", err)
	}
}

// A second run must change nothing and say so — enforce is rerun on every
// upgrade, and a noisy or file-churning rerun reads as a bug.
func TestEnforceIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())

	if _, stderr, code := runEnforce(t, homeEnv(home)); code != 0 {
		t.Fatalf("first enforce exited %d, stderr: %s", code, stderr)
	}
	claude, codex, opencode := enforcedPaths(home)
	before := map[string][]byte{}
	for _, p := range []string{claude, codex, opencode} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		before[p] = b
	}

	stdout, stderr, code := runEnforce(t, homeEnv(home))
	if code != 0 {
		t.Fatalf("second enforce exited %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "unchanged") {
		t.Fatalf("second run should report unchanged, got:\n%s", stdout)
	}
	for p, b := range before {
		after, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, after) {
			t.Fatalf("%s changed on a rerun", p)
		}
	}
}

// The settings file belongs to the user; enforce adds one hook and touches
// nothing else — not their model, not their other hooks, not an existing
// SessionStart entry.
func TestEnforcePreservesExistingClaudeSettings(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())
	claude, _, _ := enforcedPaths(home)
	seed := `{
  "model": "opus",
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "echo hi"}]}],
    "PostToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true"}]}]
  }
}`
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, code := runEnforce(t, homeEnv(home)); code != 0 {
		t.Fatalf("enforce exited %d, stderr: %s", code, stderr)
	}

	raw, err := os.ReadFile(claude)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"model"`, "opus", "echo hi", "PostToolUse", "taskr skill nudge"} {
		if !strings.Contains(text, want) {
			t.Fatalf("settings lost %q after enforce:\n%s", want, text)
		}
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings are not valid JSON after enforce: %v", err)
	}
}

// A file we cannot parse is a file we must not rewrite: a malformed
// settings.json already disables the user's hooks, and "enforce fixed it by
// replacing my config" is worse than any missing nudge.
func TestEnforceRefusesAMalformedSettingsFile(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())
	claude, _, _ := enforcedPaths(home)
	if err := os.MkdirAll(filepath.Dir(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := []byte("{not json")
	if err := os.WriteFile(claude, broken, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, code := runEnforce(t, homeEnv(home))
	if code == 0 {
		t.Fatal("enforce should fail on a settings file it cannot parse")
	}
	after, err := os.ReadFile(claude)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(broken, after) {
		t.Fatal("enforce rewrote a settings file it could not parse")
	}
}

// The marked block is replaced in place on upgrade; everything the user
// wrote around it survives byte for byte.
func TestEnforceRewritesAStaleBlockInPlace(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())
	_, codex, _ := enforcedPaths(home)
	if err := os.MkdirAll(filepath.Dir(codex), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "# My rules\n\nnever push to main\n\n" +
		enforceBegin + "\nan old directive\n" + enforceEnd + "\n\ntrailing note\n"
	if err := os.WriteFile(codex, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runEnforce(t, homeEnv(home))
	if code != 0 {
		t.Fatalf("enforce exited %d, stderr: %s", code, stderr)
	}
	raw, err := os.ReadFile(codex)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"# My rules", "never push to main", "trailing note", "taskr context"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost %q when rewriting the block:\n%s", want, text)
		}
	}
	if strings.Contains(text, "an old directive") {
		t.Fatalf("stale directive survived the rewrite:\n%s", text)
	}
	if strings.Count(text, enforceBegin) != 1 {
		t.Fatalf("want exactly one taskr block, got:\n%s", text)
	}
	if !strings.Contains(stdout, "updated") {
		t.Fatalf("a rewrite should report updated, got:\n%s", stdout)
	}
}

// --dry-run answers "what would this touch" without touching it.
func TestEnforceDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())

	stdout, stderr, code := runEnforce(t, homeEnv(home), "--dry-run")
	if code != 0 {
		t.Fatalf("enforce --dry-run exited %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "would install") {
		t.Fatalf("dry run should say what it would do, got:\n%s", stdout)
	}
	claude, codex, opencode := enforcedPaths(home)
	for _, p := range []string{claude, codex, opencode} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("dry run wrote %s", p)
		}
	}
}

// Cursor has no global rules file a CLI can write, so the rule is planted
// in the repository the user is standing in — and only in a repository.
func TestEnforceWritesTheCursorRuleInsideARepository(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub) // the rule belongs at the repository root, not the cwd

	if _, stderr, code := runEnforce(t, homeEnv(home)); code != 0 {
		t.Fatalf("enforce exited %d, stderr: %s", code, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(repo, ".cursor", "rules", "taskr.mdc"))
	if err != nil {
		t.Fatalf("want the cursor rule at the repository root: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "alwaysApply: true") {
		t.Fatalf("cursor rule is not always-apply:\n%s", text)
	}
	if !strings.Contains(text, "taskr context") {
		t.Fatalf("cursor rule does not tell the agent to orient:\n%s", text)
	}
}

// A dotfiles repository at ~ must not count as "inside a repository":
// Cursor never reads ~/.cursor/rules, so a rule planted there is a file
// that lies about doing something.
func TestEnforceSkipsTheCursorRuleWhenHomeIsTheRepository(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "somewhere")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	stdout, stderr, code := runEnforce(t, homeEnv(home))
	if code != 0 {
		t.Fatalf("enforce exited %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "skipped") {
		t.Fatalf("a dotfiles repo at home should not get the cursor rule, got:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("enforce planted a cursor rule under home")
	}
}

// The nudge is what the Claude Code hook prints into every session; it has
// to point at orientation and cost one read to follow.
func TestSkillNudgePrintsTheDirective(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"skill", "nudge"}, &stdout, &stderr, homeEnv(t.TempDir())); code != 0 {
		t.Fatalf("nudge exited %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "taskr context") {
		t.Fatalf("nudge does not mention taskr context:\n%s", stdout.String())
	}
}
