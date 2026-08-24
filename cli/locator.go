// cli/locator.go
package cli

import (
	"path/filepath"
	"strings"
)

// Locator is the wire shape every routed write and read carries: which repo
// the caller is in, and where inside it.
type Locator struct {
	RemoteURL string `json:"remote_url,omitempty"`
	Subpath   string `json:"subpath,omitempty"`
}

// LocatorFrom builds a locator from the environment and a working directory.
//
// TASKR_REMOTE and TASKR_ROOT are what the caller exported if they did, and
// otherwise what envWithRepo already read out of .git for them (repo.go) —
// either way they arrive here as plain variables, and nothing here runs
// git. The subpath is derived rather than asked for, because the caller
// would only compute it the same way.
//
// A missing TASKR_ROOT, or a working directory outside it, yields no subpath
// rather than a guess — a wrong subpath would route work into a neighbouring
// project, which is worse than resolving on the remote alone.
func LocatorFrom(getenv func(string) string, workdir string) Locator {
	l := Locator{RemoteURL: strings.TrimSpace(getenv("TASKR_REMOTE"))}
	root := strings.TrimSpace(getenv("TASKR_ROOT"))
	if root == "" || workdir == "" {
		return l
	}
	rel, err := filepath.Rel(root, workdir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return l
	}
	l.Subpath = filepath.ToSlash(rel)
	return l
}
