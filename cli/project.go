// cli/project.go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

// runProject dispatches `taskr project <ls|init|attach|rename>`. Registration
// existed only as HTTP endpoints before this, which is why no second project
// was ever created: the only way in was hand-written curl.
func runProject(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: taskr project <ls|init|attach|rename>")
	}
	switch args[0] {
	case "ls":
		return projectLs(ctx, c, args[1:], stdout, stderr)
	case "init":
		return projectInit(ctx, c, args[1:], stdout, stderr)
	case "attach":
		return projectAttach(ctx, c, args[1:], stdout, stderr, getenv)
	case "rename":
		return projectRename(ctx, c, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown project command %q; want ls, init, attach, or rename", args[0])
	}
}

// projectLs is what an agent reads to decide whether a project already
// covers where it is standing, so it renders every project's repos and dirs
// alongside its identity rather than just the bare list ListProjects itself
// returns.
func projectLs(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	rows, err := c.ListProjects(ctx)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, rows)
	}
	RenderProjects(stdout, rows)
	return nil
}

// projectInit is the one command that can create a project a curl script
// used to be the only way to reach — SetupProject is idempotent on slug, so
// re-running this is always safe, but Key is required the first time.
func projectInit(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "issue ref prefix, e.g. TSK; required the first time a project is created")
	name := fs.String("name", "", "display name; defaults to the slug")
	branchFormat := fs.String("branch-format", "", "branch name shape, e.g. tc/{key}-{n}--{slug}")
	commitStyle := fs.String("commit-style", "", "commit message style, e.g. conventional")
	prTarget := fs.String("pr-target", "", "branch PRs are opened against, e.g. master")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr project init <slug> --key KEY [--name N] " +
			"[--branch-format F] [--commit-style S] [--pr-target BRANCH]")
	}

	res, err := c.SetupProject(ctx, SetupProjectInput{
		Slug: positional[0], Key: *key, Name: *name,
		Conventions: conventionsFrom(*branchFormat, *commitStyle, *prTarget),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, res)
	}
	fmt.Fprintf(stdout, "Initialized %s (%s).\n", positional[0], res.Key)
	return nil
}

// conventionsFrom returns nil when the caller named no convention at all,
// so `project init` on an existing project stays a no-op on them. Sending
// an empty block would be harmless against today's server — its upsert
// skips empty values — but nil is what makes that independent of a rule
// living in another repo.
func conventionsFrom(branchFormat, commitStyle, prTarget string) *ProjectConventions {
	c := ProjectConventions{
		BranchFormat: strings.TrimSpace(branchFormat),
		CommitStyle:  strings.TrimSpace(commitStyle),
		PRTarget:     strings.TrimSpace(prTarget),
	}
	if c == (ProjectConventions{}) {
		return nil
	}
	return &c
}

// projectAttach defaults its project to whatever the caller's locator
// resolves to and its repo to TASKR_REMOTE, so onboarding from inside the
// repo needs no arguments the caller would only have to look up.
func projectAttach(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("project attach", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project slug; defaults to the one this directory resolves to")
	repo := fs.String("repo", "", "git remote; defaults to TASKR_REMOTE")
	dir := fs.String("dir", "", "repo-relative directory this project owns")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	loc := LocatorFrom(getenv, cwd())
	remote := *repo
	if remote == "" {
		remote = loc.RemoteURL
	}
	if remote == "" {
		return fmt.Errorf("no repo given and TASKR_REMOTE is unset; pass --repo")
	}
	slug := *project
	if slug == "" {
		v, err := c.Context(ctx, ContextQuery{RemoteURL: loc.RemoteURL})
		if err != nil {
			return err
		}
		if v.Project == nil {
			return fmt.Errorf("no --project given and this directory resolves to none; pass --project")
		}
		slug = v.Project.Slug
	}

	if err := c.AttachRepo(ctx, slug, AttachRepoInput{RemoteURL: remote, Subpath: strings.TrimSpace(*dir)}); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, okResult{OK: true})
	}
	if *dir != "" {
		fmt.Fprintf(stdout, "Attached %s (%s) to %s.\n", remote, *dir, slug)
		return nil
	}
	fmt.Fprintf(stdout, "Attached %s to %s.\n", remote, slug)
	return nil
}

// projectRename changes a project's slug and, optionally, its display name.
// The key and every issue number are untouched — refs are permanent, and
// renumbering would break every reference anyone has written down.
func projectRename(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project rename", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "display name; defaults to the new slug")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return fmt.Errorf("usage: taskr project rename <slug> <new-slug> [--name N]")
	}
	slug, newSlug := positional[0], positional[1]

	if err := c.RenameProject(ctx, slug, newSlug, *name); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, okResult{OK: true})
	}
	fmt.Fprintf(stdout, "Renamed %s to %s.\n", slug, newSlug)
	return nil
}
