// cli/skill.go
package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/thomas-cabral/taskr-cli/skills"
)

// skillTargets are the directories agent harnesses read skills out of.
//
// Two directories cover the four harnesses that matter, which is why this
// is a list and not a detection routine:
//
//	~/.agents/skills   Codex, Cursor and opencode
//	~/.claude/skills   Claude Code, which reads only its own
//
// Both are written whether or not the matching harness is installed. A
// SKILL.md in a directory nothing reads costs a few kilobytes; the opposite
// mistake — installing for the harness the user had today and not the one
// they switch to next month — costs them a silent, undiagnosable gap where
// the agent simply never learns taskr exists.
//
// Per-harness directories (.cursor/skills, $CODEX_HOME/skills) are
// deliberately not written: each is read by exactly one harness that
// already reads one of the two above.
var skillTargets = []string{
	filepath.Join(".agents", "skills"),
	filepath.Join(".claude", "skills"),
}

// runSkill dispatches `taskr skill`. It answers without a target or a
// credential — install.sh runs it seconds after the binary lands, before
// `taskr auth login` has been typed, and a skill install that demanded a
// key would fail on every fresh machine.
func runSkill(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taskr skill install [--dir <path>] [--dry-run]\n       taskr skill enforce [--dry-run]\n       taskr skill ls")
	}
	switch args[0] {
	case "install":
		return cmdSkillInstall(args[1:], stdout, stderr, getenv)
	case "enforce":
		return cmdSkillEnforce(args[1:], stdout, stderr, getenv)
	case "nudge":
		return cmdSkillNudge(stdout, getenv)
	case "ls":
		return cmdSkillLs(args[1:], stdout, getenv)
	default:
		return fmt.Errorf("unknown skill command %q — try install, enforce or ls", args[0])
	}
}

// skillResult is one skill written to one directory, and what it did there.
type skillResult struct {
	Skill  string `json:"skill"`
	Path   string `json:"path"`
	Status string `json:"status"` // installed, updated, unchanged, missing, modified
}

func cmdSkillInstall(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("skill install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "install into this directory instead of the harness defaults")
	dryRun := fs.Bool("dry-run", false, "report what would be written without writing it")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	roots, err := skillRoots(*dir, getenv)
	if err != nil {
		return err
	}

	var results []skillResult
	for _, root := range roots {
		for _, name := range skills.Names {
			body, err := skills.Body(name)
			if err != nil {
				return err
			}
			path := filepath.Join(root, name, "SKILL.md")
			status, err := writeSkill(path, body, *dryRun)
			if err != nil {
				return err
			}
			results = append(results, skillResult{Skill: name, Path: path, Status: status})
		}
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(results)
	}
	for _, r := range results {
		fmt.Fprintf(stdout, "%-13s %s\n", r.Status, r.Path)
	}
	if *dryRun {
		return nil
	}
	// The skills are found by description, so the next agent picks them up
	// on its next start — not in a session that is already running. Saying
	// so here is cheaper than the user concluding the install did nothing.
	fmt.Fprintln(stdout, "\nRestart your agent session to pick them up.")
	return nil
}

func cmdSkillLs(args []string, stdout io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("skill ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	roots, err := skillRoots("", getenv)
	if err != nil {
		return err
	}

	var results []skillResult
	for _, root := range roots {
		for _, name := range skills.Names {
			body, err := skills.Body(name)
			if err != nil {
				return err
			}
			path := filepath.Join(root, name, "SKILL.md")
			results = append(results, skillResult{Skill: name, Path: path, Status: skillStatus(path, body)})
		}
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(results)
	}
	for _, r := range results {
		fmt.Fprintf(stdout, "%-13s %s\n", r.Status, r.Path)
	}
	return nil
}

// skillRoots resolves where the skills go. An explicit --dir is taken
// verbatim, which is what a monorepo checking a copy into the tree wants;
// otherwise both harness directories under the user's home.
func skillRoots(dir string, getenv func(string) string) ([]string, error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		return []string{abs}, nil
	}
	home, err := homeDir(getenv)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(skillTargets))
	for _, t := range skillTargets {
		roots = append(roots, filepath.Join(home, t))
	}
	return roots, nil
}

// homeDir asks the environment first so a test — and an installer running
// as another user — can say where home is without the answer coming from
// the passwd database of whoever happens to own the process.
func homeDir(getenv func(string) string) (string, error) {
	if h := getenv("HOME"); h != "" {
		return h, nil
	}
	return os.UserHomeDir()
}

// writeSkill puts one skill on disk and reports what changed, so a second
// install is visibly a no-op rather than an unreadable wall of successes.
func writeSkill(path string, body []byte, dryRun bool) (string, error) {
	status := skillStatus(path, body)
	if status == "unchanged" {
		return status, nil
	}
	done, would := "installed", "would install"
	if status == "modified" {
		done, would = "updated", "would update"
	}
	if dryRun {
		return would, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return done, nil
}

// skillStatus compares what is on disk with what this binary carries.
//
// The comparison is the whole point: a skill documents the verbs of the
// binary that installed it, so an installed copy older than the running
// taskr is a skill describing flags that may no longer exist. Comparing
// bytes needs no version marker to go stale on its own.
func skillStatus(path string, body []byte) string {
	existing, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	if bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(body)) {
		return "unchanged"
	}
	return "modified"
}
