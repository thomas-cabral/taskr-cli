// cli/enforce.go
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// enforceDirective is the session-start nudge every shim delivers. Skills
// are found by description, which leaves loading to the model's judgment;
// this one paragraph is the part that is not left to judgment. It has to
// stay short — it is injected into every session on the machine — and it
// has to point somewhere, because a nudge that cannot be followed in one
// step is scenery.
const enforceDirective = "taskr is installed on this machine. Before starting any work, run " +
	"`taskr context` — it reports whether a work session is already in progress " +
	"and what it was doing. Follow the taskr skill (orient, pick, step, offload, " +
	"park; installed at ~/.agents/skills/taskr/SKILL.md). When you notice work " +
	"that is not what you are working on, `taskr offload` it instead of fixing " +
	"it inline."

// The markers fencing the directive inside a file the user also writes.
// Everything between them belongs to taskr and is rewritten on upgrade;
// everything outside them is never touched.
const (
	enforceBegin = "<!-- taskr:enforce:begin -->"
	enforceEnd   = "<!-- taskr:enforce:end -->"
)

// nudgeCommand is what the Claude Code hook runs. The directive itself
// deliberately does not live in settings.json: text written there once
// would say whatever the binary said the day enforce ran, forever. The
// binary prints it instead, so the nudge upgrades with the CLI — and the
// guard keeps a machine that later loses taskr from a hook error in every
// session.
const nudgeCommand = "taskr skill nudge 2>/dev/null || true"

// enforceResult is one shim written for one harness, and what happened.
type enforceResult struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	Status string `json:"status"`
}

func cmdSkillEnforce(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("skill enforce", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "report what would be written without writing it")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	home, err := homeDir(getenv)
	if err != nil {
		return err
	}

	var results []enforceResult
	add := func(target, path, status string) {
		results = append(results, enforceResult{Target: target, Path: path, Status: status})
	}

	status, err := enforceClaudeHook(filepath.Join(home, ".claude", "settings.json"), *dryRun)
	if err != nil {
		return err
	}
	add("claude-code", filepath.Join(home, ".claude", "settings.json"), status)

	codexPath := filepath.Join(home, ".codex", "AGENTS.md")
	if status, err = enforceMarkedBlock(codexPath, *dryRun); err != nil {
		return err
	}
	add("codex", codexPath, status)

	opencodePath := filepath.Join(configHome(getenv, home), "opencode", "AGENTS.md")
	if status, err = enforceMarkedBlock(opencodePath, *dryRun); err != nil {
		return err
	}
	add("opencode", opencodePath, status)

	if root := repoRoot(home); root == "" {
		add("cursor", "", "skipped (not inside a git repository; rerun from a repo to plant .cursor/rules/taskr.mdc)")
	} else {
		rulePath := filepath.Join(root, ".cursor", "rules", "taskr.mdc")
		if status, err = writeSkill(rulePath, []byte(cursorRule()), *dryRun); err != nil {
			return err
		}
		add("cursor", rulePath, status)
	}

	if *jsonOut {
		return json.NewEncoder(stdout).Encode(results)
	}
	for _, r := range results {
		fmt.Fprintf(stdout, "%-13s %-11s %s\n", r.Status, r.Target, r.Path)
	}
	if !*dryRun {
		fmt.Fprintln(stdout, "\nNew sessions in each harness now start with the taskr nudge.")
	}
	return nil
}

// cmdSkillNudge prints the directive. It exists to be called from the
// Claude Code SessionStart hook that enforce installs; a human running it
// by hand just sees what their agents see.
func cmdSkillNudge(stdout io.Writer) error {
	_, err := fmt.Fprintln(stdout, enforceDirective)
	return err
}

// enforceClaudeHook merges one SessionStart hook into Claude Code's user
// settings. The file is the user's — the merge adds exactly one entry,
// preserves everything else, and refuses a file it cannot parse rather
// than "fixing" it: a clobbered settings.json costs the user every hook
// and permission rule they had.
func enforceClaudeHook(path string, dryRun bool) (string, error) {
	settings := map[string]any{}
	raw, err := os.ReadFile(path)
	existed := err == nil
	if existed {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", fmt.Errorf("%s is not valid JSON (%v) — fix it, then rerun enforce", path, err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		if _, present := settings["hooks"]; present {
			return "", fmt.Errorf("%s has a \"hooks\" key that is not an object — refusing to rewrite it", path)
		}
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	starts, ok := hooks["SessionStart"].([]any)
	if !ok {
		if _, present := hooks["SessionStart"]; present {
			return "", fmt.Errorf("%s has a \"SessionStart\" entry that is not an array — refusing to rewrite it", path)
		}
	}
	for _, entry := range starts {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if ok && hm["command"] == nudgeCommand {
				return "unchanged", nil
			}
		}
	}

	status := "updated"
	if !existed {
		status = "installed"
	}
	if dryRun {
		return "would " + map[string]string{"updated": "update", "installed": "install"}[status], nil
	}

	hooks["SessionStart"] = append(starts, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": nudgeCommand}},
	})
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return "", err
	}
	return status, nil
}

// enforceMarkedBlock puts the directive between the taskr markers in an
// instructions file the harness loads unconditionally, creating the file
// when it is absent and rewriting only what sits between the markers when
// it is not.
func enforceMarkedBlock(path string, dryRun bool) (string, error) {
	block := enforceBegin + "\n" + enforceDirective + "\n" + enforceEnd

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if dryRun {
			return "would install", nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(block+"\n"), 0o644); err != nil {
			return "", err
		}
		return "installed", nil
	}
	if err != nil {
		return "", err
	}

	text := string(raw)
	begin := strings.Index(text, enforceBegin)
	end := strings.Index(text, enforceEnd)
	var updated string
	switch {
	case begin == -1 && end == -1:
		updated = strings.TrimRight(text, "\n") + "\n\n" + block + "\n"
	case begin == -1 || end == -1 || end < begin:
		return "", fmt.Errorf("%s has a mangled taskr block — remove the stray marker, then rerun enforce", path)
	default:
		if text[begin:end+len(enforceEnd)] == block {
			return "unchanged", nil
		}
		updated = text[:begin] + block + text[end+len(enforceEnd):]
	}

	status := "updated"
	if begin == -1 {
		status = "installed"
	}
	if dryRun {
		return "would " + map[string]string{"updated": "update", "installed": "install"}[status], nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return status, nil
}

// cursorRule is the always-apply rule Cursor loads into every session of
// the repository it is written into. Cursor has no global rules file a CLI
// can write — rules are per-repository by design — which is why this one
// shim is planted where the user is standing instead of under home.
func cursorRule() string {
	return "---\nalwaysApply: true\n---\n\n" + enforceDirective + "\n"
}

// configHome is where XDG-following tools such as opencode keep their
// config: $XDG_CONFIG_HOME when set, ~/.config when not.
func configHome(getenv func(string) string, home string) string {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	return filepath.Join(home, ".config")
}

// repoRoot walks up from the working directory to the nearest directory
// holding a .git entry — a directory in a checkout, a file in a worktree,
// either counts. Empty when there is none. The home directory itself does
// not count even when it has a .git: a dotfiles repo at ~ would otherwise
// turn every shell into "inside a repository" and plant the cursor rule at
// ~/.cursor/rules, a directory Cursor never reads.
func repoRoot(home string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if dir == home {
			return ""
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
