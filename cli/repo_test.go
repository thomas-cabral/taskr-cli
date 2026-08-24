// cli/repo_test.go
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	headSHA   = "1f0c9a2b3c4d5e6f708192a3b4c5d6e7f8091a2b"
	packedSHA = "abcdef0123456789abcdef0123456789abcdef01"
)

// writeGitFile writes one fixture file, creating parents. Every fixture
// here is a handful of small files, which is exactly what a .git is.
func writeGitFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// plainRepo builds the shape `git clone` leaves behind: a .git directory
// with a config naming origin, a HEAD pointing at a branch, and that
// branch as a loose ref.
func plainRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	writeGitFile(t, filepath.Join(git, "config"), `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = https://github.com/thomas-cabral/taskr-cli.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[branch "master"]
	remote = origin
`)
	writeGitFile(t, filepath.Join(git, "HEAD"), "ref: refs/heads/master\n")
	writeGitFile(t, filepath.Join(git, "refs", "heads", "master"), headSHA+"\n")
	return root
}

func TestDiscoverRepoReadsACheckout(t *testing.T) {
	root := plainRepo(t)
	got := discoverRepo(root)
	want := repoFacts{
		Root:   root,
		Remote: "https://github.com/thomas-cabral/taskr-cli.git",
		Head:   headSHA,
		Branch: "master",
	}
	if got != want {
		t.Fatalf("discoverRepo() = %+v, want %+v", got, want)
	}
}

// The whole point of the walk: an agent's cwd is usually a directory deep
// inside the repo, not its root. The root reported has to be the repo's,
// because that is what LocatorFrom subtracts to get the subpath that routes
// a write to the right project in a monorepo.
func TestDiscoverRepoWalksUpFromASubdirectory(t *testing.T) {
	root := plainRepo(t)
	deep := filepath.Join(root, "cli", "internal")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := discoverRepo(deep); got.Root != root || got.Head != headSHA {
		t.Fatalf("discoverRepo(subdir) = %+v, want root %q and head %q", got, root, headSHA)
	}
}

// A fresh clone has no loose refs at all — every branch lives in
// packed-refs until something writes to it. A reader that only knows
// refs/heads/ reports no head for a repo the caller just cloned.
func TestDiscoverRepoResolvesAPackedBranch(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	writeGitFile(t, filepath.Join(git, "config"), "[remote \"origin\"]\n\turl = git@github.com:you/repo.git\n")
	writeGitFile(t, filepath.Join(git, "HEAD"), "ref: refs/heads/main\n")
	writeGitFile(t, filepath.Join(git, "packed-refs"), `# pack-refs with: peeled fully-peeled sorted
`+packedSHA+` refs/heads/main
0000000000000000000000000000000000000000 refs/tags/v1
^`+headSHA+`
`)
	got := discoverRepo(root)
	if got.Head != packedSHA || got.Branch != "main" {
		t.Fatalf("discoverRepo() = %+v, want head %q on branch main", got, packedSHA)
	}
}

// A detached HEAD keeps its commit and loses only the branch. Dropping the
// head too would cost the resume packet the one field it anchors on.
func TestDiscoverRepoHandlesDetachedHEAD(t *testing.T) {
	root := plainRepo(t)
	writeGitFile(t, filepath.Join(root, ".git", "HEAD"), headSHA+"\n")
	got := discoverRepo(root)
	if got.Head != headSHA || got.Branch != "" {
		t.Fatalf("discoverRepo() = %+v, want head %q and no branch", got, headSHA)
	}
}

// `git worktree add` writes .git as a FILE naming a directory elsewhere,
// and that directory shares config and packed refs with the main checkout
// through commondir. Both halves have to be followed or every worktree
// resolves a remote of "" and looks unregistered — which is the shape agent
// sessions actually run in, since they are given worktrees to work in.
func TestDiscoverRepoFollowsALinkedWorktree(t *testing.T) {
	main := plainRepo(t)
	linked := t.TempDir()
	wt := filepath.Join(main, ".git", "worktrees", "feature")
	writeGitFile(t, filepath.Join(linked, ".git"), "gitdir: "+wt+"\n")
	writeGitFile(t, filepath.Join(wt, "commondir"), "../..\n")
	writeGitFile(t, filepath.Join(wt, "HEAD"), "ref: refs/heads/feature\n")
	writeGitFile(t, filepath.Join(main, ".git", "refs", "heads", "feature"), packedSHA+"\n")

	got := discoverRepo(linked)
	want := repoFacts{
		Root:   linked,
		Remote: "https://github.com/thomas-cabral/taskr-cli.git",
		Head:   packedSHA,
		Branch: "feature",
	}
	if got != want {
		t.Fatalf("discoverRepo(worktree) = %+v, want %+v", got, want)
	}
}

// Between `git init` and the first commit the branch exists and the commit
// does not. Reporting a branch with no head is the honest answer; inventing
// a head would put a commit that does not exist onto an issue.
func TestDiscoverRepoReportsABranchWithNoCommit(t *testing.T) {
	root := t.TempDir()
	writeGitFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/master\n")
	writeGitFile(t, filepath.Join(root, ".git", "config"), "[core]\n\tbare = false\n")
	got := discoverRepo(root)
	if got.Head != "" || got.Branch != "master" || got.Remote != "" {
		t.Fatalf("discoverRepo() = %+v, want branch master, no head, no remote", got)
	}
}

// A repo whose only remote is named something else resolves no remote at
// all. Taking whichever remote happens to be listed would route the work
// into a fork's project.
func TestDiscoverRepoIgnoresNonOriginRemotes(t *testing.T) {
	root := plainRepo(t)
	writeGitFile(t, filepath.Join(root, ".git", "config"), "[remote \"upstream\"]\n\turl = git@github.com:someone/else.git\n")
	if got := discoverRepo(root); got.Remote != "" {
		t.Fatalf("discoverRepo().Remote = %q, want empty for a repo with no origin", got.Remote)
	}
}

func TestDiscoverRepoOutsideAnyCheckout(t *testing.T) {
	if got := discoverRepo(t.TempDir()); got != (repoFacts{}) {
		t.Fatalf("discoverRepo() = %+v, want zero facts outside a repo", got)
	}
}

// The environment is the caller speaking, and discovery is a fallback for
// when they did not. A caller who exports TASKR_REMOTE to file work against
// another repo must not have it overruled by the directory they happen to
// be standing in.
func TestEnvWithRepoPrefersTheEnvironment(t *testing.T) {
	root := plainRepo(t)
	env := map[string]string{
		"TASKR_REMOTE": "git@github.com:you/other.git",
		"TASKR_KEY":    "tk_live_123",
	}
	getenv := envWithRepo(func(k string) string { return env[k] }, root)

	if got := getenv("TASKR_REMOTE"); got != "git@github.com:you/other.git" {
		t.Fatalf("TASKR_REMOTE = %q, want the exported value to win", got)
	}
	if got := getenv("TASKR_HEAD"); got != headSHA {
		t.Fatalf("TASKR_HEAD = %q, want the checkout's head %q", got, headSHA)
	}
	if got := getenv("TASKR_KEY"); got != "tk_live_123" {
		t.Fatalf("TASKR_KEY = %q, want variables it does not discover passed through", got)
	}
	if got := getenv("TASKR_MERGE_BASE"); got != "" {
		t.Fatalf("TASKR_MERGE_BASE = %q, want empty: a merge base is not a file read", got)
	}
}

// What the fallback is for, end to end: with nothing exported, a write from
// a subdirectory still carries the repo AND the subpath that routes it.
func TestLocatorFromResolvesWithNothingExported(t *testing.T) {
	root := plainRepo(t)
	workdir := filepath.Join(root, "web", "src")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	getenv := envWithRepo(func(string) string { return "" }, workdir)

	got := LocatorFrom(getenv, workdir)
	want := Locator{RemoteURL: "https://github.com/thomas-cabral/taskr-cli.git", Subpath: "web/src"}
	if got != want {
		t.Fatalf("LocatorFrom() = %+v, want %+v", got, want)
	}
}

// The snapshot new/offload/park record is gated on a head. Discovering one
// is what turns "no git snapshot has been recorded for this issue" — the
// state every issue filed from an agent harness was left in — into a real
// snapshot, without the caller exporting anything.
func TestGitSnapshotFromADiscoveredCheckout(t *testing.T) {
	root := plainRepo(t)
	getenv := envWithRepo(func(string) string { return "" }, root)

	snap := gitSnapshot(getenv)
	if snap == nil {
		t.Fatal("gitSnapshot() = nil, want a snapshot discovered from the checkout")
	}
	if snap.HeadSHA != headSHA || snap.Branch != "master" || snap.Worktree != root {
		t.Fatalf("gitSnapshot() = %+v, want head %q, branch master, worktree %q", snap, headSHA, root)
	}
	if snap.MergeBase != "" || len(snap.DirtyFiles) != 0 {
		t.Fatalf("gitSnapshot() = %+v, want merge base and dirty files left to the caller", snap)
	}
}
