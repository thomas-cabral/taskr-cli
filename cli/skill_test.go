// cli/skill_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thomas-cabral/taskr-cli/skills"
)

// homeEnv is an environment with nothing in it but a home directory, which
// is the environment `taskr skill install` actually runs in: install.sh
// calls it on a machine that has no key, no API and no checkout.
func homeEnv(home string) func(string) string {
	return func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
}

// The contract the installer depends on: one command, and every harness on
// the machine can find the skill. Both directories, both skills, no key.
func TestSkillInstallWritesBothHarnessDirectories(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"skill", "install"}, &stdout, &stderr, homeEnv(home)); code != 0 {
		t.Fatalf("skill install exited %d, stderr: %s", code, stderr.String())
	}

	for _, root := range []string{".agents/skills", ".claude/skills"} {
		for _, name := range skills.Names {
			path := filepath.Join(home, filepath.FromSlash(root), name, "SKILL.md")
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("want %s installed: %v", path, err)
			}
			want, err := skills.Body(name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s does not match the embedded copy", path)
			}
		}
	}
}

// Every skill this binary carries has to have the frontmatter every harness
// requires. Codex and Cursor both refuse a skill without name and
// description, and opencode's loader drops it silently — a failure nobody
// sees until an agent never uses the skill.
func TestEmbeddedSkillsCarryTheRequiredFrontmatter(t *testing.T) {
	for _, name := range skills.Names {
		body, err := skills.Body(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(body)
		if !strings.HasPrefix(text, "---\n") {
			t.Fatalf("%s: want YAML frontmatter at the top of SKILL.md", name)
		}
		front, _, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---")
		if !ok {
			t.Fatalf("%s: frontmatter is not terminated", name)
		}
		if !strings.Contains(front, "name: "+name) {
			t.Fatalf("%s: frontmatter name must match the directory, got %q", name, front)
		}
		if !strings.Contains(front, "description: ") {
			t.Fatalf("%s: frontmatter has no description — harnesses route on it", name)
		}
	}
}

// A second install is a no-op, and says so. install.sh runs this on every
// upgrade, so the common case is "nothing changed" and it must not read
// like work was done.
func TestSkillInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	var first, second bytes.Buffer

	Run([]string{"skill", "install"}, &first, &first, homeEnv(home))
	if !strings.Contains(first.String(), "installed") {
		t.Fatalf("first install said %q, want it to report installing", first.String())
	}
	if code := Run([]string{"skill", "install"}, &second, &second, homeEnv(home)); code != 0 {
		t.Fatalf("second install exited %d: %s", code, second.String())
	}
	if strings.Contains(second.String(), "installed ") || !strings.Contains(second.String(), "unchanged") {
		t.Fatalf("second install said %q, want every line unchanged", second.String())
	}
}

// An older skill on disk is the failure this whole move exists to stop: it
// documents verbs the running binary may no longer have. Install overwrites
// it, and ls names it before that as modified.
func TestSkillInstallReplacesAStaleCopy(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".agents", "skills", "taskr", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: taskr\n---\n\nan older copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var ls bytes.Buffer
	Run([]string{"skill", "ls"}, &ls, &ls, homeEnv(home))
	if !strings.Contains(ls.String(), "modified") {
		t.Fatalf("skill ls said %q, want the stale copy reported as modified", ls.String())
	}

	var out bytes.Buffer
	Run([]string{"skill", "install"}, &out, &out, homeEnv(home))
	if !strings.Contains(out.String(), "updated") {
		t.Fatalf("skill install said %q, want it to report updating", out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := skills.Body("taskr")
	if !bytes.Equal(got, want) {
		t.Fatal("the stale copy survived install")
	}
}

// --dry-run has to write nothing at all. It is what a user runs when they
// are not yet sure they want files in their home directory.
func TestSkillInstallDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer

	if code := Run([]string{"skill", "install", "--dry-run"}, &out, &out, homeEnv(home)); code != 0 {
		t.Fatalf("dry run exited %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "would install") {
		t.Fatalf("dry run said %q, want it to report what it would do", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatal("dry run created directories")
	}
}

func TestSkillInstallDirOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo", ".claude", "skills")
	home := t.TempDir()
	var out bytes.Buffer

	if code := Run([]string{"skill", "install", "--dir", dir}, &out, &out, homeEnv(home)); code != 0 {
		t.Fatalf("skill install --dir exited %d: %s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "taskr", "SKILL.md")); err != nil {
		t.Fatalf("want the skill in --dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatal("--dir also wrote the default directories")
	}
}

func TestSkillLsJSONReportsEveryTarget(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer

	if code := Run([]string{"skill", "ls", "--json"}, &out, &out, homeEnv(home)); code != 0 {
		t.Fatalf("skill ls --json exited %d: %s", code, out.String())
	}
	var results []skillResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("skill ls --json is not JSON: %v (%s)", err, out.String())
	}
	if len(results) != 2*len(skills.Names) {
		t.Fatalf("got %d rows, want one per skill per harness directory", len(results))
	}
	for _, r := range results {
		if r.Status != "missing" {
			t.Fatalf("%+v: want missing before anything is installed", r)
		}
	}
}
