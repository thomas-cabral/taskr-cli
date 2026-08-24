// cli/repo.go
package cli

import (
	"bufio"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// repoFacts is what the CLI can work out about the checkout it is standing
// in without help from the caller.
//
// Everything here is READ out of the files git keeps, never asked of git
// itself: no subprocess is started, no git binary has to exist on the
// machine, and a caller whose PATH has no git still resolves a project.
// That is the line this file holds. Anything that would need git's own
// machinery to answer — a merge base needs a commit-graph walk, a dirty
// list needs the index diffed against the worktree — is deliberately NOT
// here, and stays what it has always been: something the caller reports
// through TASKR_MERGE_BASE and TASKR_DIRTY.
type repoFacts struct {
	Root   string // the worktree root: the directory holding .git
	Remote string // origin's URL, verbatim from .git/config
	Head   string // the commit HEAD resolves to
	Branch string // HEAD's branch, or "" when detached
}

// discoverRepo reads the checkout containing workdir.
//
// It exists because the environment contract it backs up cannot be relied
// on. TASKR_REMOTE, TASKR_ROOT, TASKR_HEAD and TASKR_BRANCH are exports the
// caller is told to make once per session — which holds only for a caller
// that HAS a session, i.e. a shell that persists between commands. An agent
// harness generally does not give its tool calls one: each call is its own
// process, the exports evaporate with it, and every taskr write after the
// first arrives with no repo attached at all. The failure is silent — an
// issue filed against no project, or a snapshot omitted entirely — which is
// the worst shape it could take.
//
// A checkout it cannot read yields a zero repoFacts rather than a guess.
// Callers treat every field as best-effort, exactly as they already treat
// an unset variable.
func discoverRepo(workdir string) repoFacts {
	root, gitDir := findGitDir(workdir)
	if gitDir == "" {
		return repoFacts{}
	}
	// A linked worktree keeps its own HEAD but shares config and packed
	// refs with the checkout it was created from; commondir is the pointer
	// to that shared directory. Without following it, every `git worktree`
	// checkout resolves a remote of "" and looks unregistered.
	common := gitDir
	if v := readTrimmed(filepath.Join(gitDir, "commondir")); v != "" {
		common = resolveGitPath(gitDir, v)
	}
	facts := repoFacts{Root: root, Remote: originURL(filepath.Join(common, "config"))}
	facts.Head, facts.Branch = headAt(gitDir, common)
	return facts
}

// findGitDir walks up from workdir looking for the .git that governs it,
// and reports the worktree root alongside it. The two are not the same
// directory for a linked worktree, where .git is a FILE naming a directory
// elsewhere — the root is still where that file lives, since that is what
// paths in an issue are relative to.
func findGitDir(workdir string) (root, gitDir string) {
	dir, err := filepath.Abs(strings.TrimSpace(workdir))
	if err != nil || workdir == "" {
		return "", ""
	}
	for {
		candidate := filepath.Join(dir, ".git")
		switch info, err := os.Stat(candidate); {
		case err == nil && info.IsDir():
			return dir, candidate
		case err == nil && info.Mode().IsRegular():
			// "gitdir: /path/to/.git/worktrees/name", written by
			// `git worktree add` and by submodule checkouts.
			line := readTrimmed(candidate)
			if rest, ok := strings.CutPrefix(line, "gitdir:"); ok {
				if p := strings.TrimSpace(rest); p != "" {
					return dir, resolveGitPath(dir, p)
				}
			}
			return "", ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "" // reached the filesystem root
		}
		dir = parent
	}
}

// resolveGitPath resolves a path git wrote into one of its own files. They
// are absolute as often as they are relative, and a relative one is
// relative to the file's own directory, not to the caller's cwd.
func resolveGitPath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

// originURL pulls remote.origin.url out of a git config file.
//
// This is a deliberately small INI reader, not a general one: it wants a
// single key, and the shapes git writes for it are `[remote "origin"]`
// followed by `url = ...`. A repo with no origin — a fresh `git init`, or a
// checkout whose only remote is named something else — yields "", which
// reads downstream as "the caller did not say", the same as an unset
// TASKR_REMOTE. Guessing at another remote would route work into whatever
// fork happened to be listed first.
func originURL(configPath string) string {
	f, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	inOrigin := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.HasPrefix(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "url" {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

// headAt resolves HEAD to a commit, and to a branch name when there is one.
//
// A detached HEAD reports the commit and no branch rather than nothing at
// all: the head is the half a resume packet actually anchors on, and
// dropping it because there is no branch to name would lose more than it
// protects. That matches how gitSnapshot already treats an unset
// TASKR_BRANCH.
func headAt(gitDir, common string) (head, branch string) {
	line := readTrimmed(filepath.Join(gitDir, "HEAD"))
	if line == "" {
		return "", ""
	}
	rest, ok := strings.CutPrefix(line, "ref:")
	if !ok {
		if isHex(line) {
			return line, "" // detached
		}
		return "", ""
	}
	ref := strings.TrimSpace(rest)
	branch = strings.TrimPrefix(ref, "refs/heads/")

	// Loose ref first, then packed-refs: `git gc` and a fresh clone both
	// leave branches packed, so a reader that only knows refs/heads/ finds
	// nothing in a repo that was cloned and never committed to.
	for _, dir := range []string{gitDir, common} {
		if sha := readTrimmed(filepath.Join(dir, filepath.FromSlash(ref))); isHex(sha) {
			return sha, branch
		}
	}
	if sha := packedRef(filepath.Join(common, "packed-refs"), ref); sha != "" {
		return sha, branch
	}
	// A branch with no commit yet — `git init` before the first commit —
	// is a real state, and it has no head to report.
	return "", branch
}

// packedRef finds one ref in a packed-refs file. Lines are "<sha> <ref>",
// with "^<sha>" peel lines for tags that this skips: it is asked for
// branches, and a peel line would answer with the wrong commit.
func packedRef(path, ref string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		sha, name, ok := strings.Cut(line, " ")
		if ok && strings.TrimSpace(name) == ref && isHex(sha) {
			return sha
		}
	}
	return ""
}

// readTrimmed reads a small git file. Unreadable and empty are the same
// answer here: "cannot tell".
func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// isHex reports whether s looks like an object id, which is what separates
// a detached HEAD from a malformed one.
func isHex(s string) bool {
	if len(s) < 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// envWithRepo layers the checkout under the environment, so an unset
// variable is answered by the repo the caller is standing in instead of
// being left blank.
//
// The environment still wins wherever it is set. That ordering is not a
// detail: TASKR_REMOTE is how a caller deliberately points a write at a
// repo other than the one their shell is in, and discovery must never
// overrule a caller who said something explicitly.
//
// Discovery runs at most once per invocation, and only if something asks
// for a variable that is missing — a call with the full environment
// exported touches the filesystem not at all.
func envWithRepo(getenv func(string) string, workdir string) func(string) string {
	var (
		facts  repoFacts
		looked bool
	)
	discover := func() repoFacts {
		if !looked {
			facts, looked = discoverRepo(workdir), true
		}
		return facts
	}
	return func(key string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		switch key {
		case "TASKR_REMOTE":
			return discover().Remote
		case "TASKR_ROOT":
			return discover().Root
		case "TASKR_HEAD":
			return discover().Head
		case "TASKR_BRANCH":
			return discover().Branch
		}
		return getenv(key)
	}
}
