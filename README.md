# taskr-cli

`taskr` is the command-line client for a [taskr](https://github.com/thomas-cabral/taskr)
server — issues, specs and plans that survive losing context, whether the
caller is a person or an agent shelling out between turns.

It is an HTTP client and nothing else: it holds no domain logic and never
opens a database. It never shells out to git either — it reads git's own
files to know which checkout you are in (origin, worktree root, HEAD,
branch), and anything that would need git itself to compute, like a merge
base or a dirty list, stays something the caller reports. This repo builds
only the `taskr` binary; it does not include the server.

## Install

With a Go toolchain:

```bash
go install github.com/thomas-cabral/taskr-cli/cmd/taskr@latest
```

macOS and Linux:

```bash
curl -fsSL https://aitaskr.com/install.sh | sh
```

This puts the binary in `~/.local/bin`; set `TASKR_INSTALL_DIR` to install
somewhere else. Not yet live — it starts working with the next site
deploy; until then, use `go install` above or the releases page below.

It also installs the agent skills, which is how a coding agent learns taskr
exists at all. Set `TASKR_SKILLS=0` to skip that, or run it yourself later:

```bash
taskr skill install    # ~/.agents/skills (Codex, Cursor, opencode)
                       # ~/.claude/skills (Claude Code)
taskr skill enforce    # session-start nudge: Claude Code hook, Codex and
                       # opencode AGENTS.md blocks, a Cursor rule in this repo
taskr skill ls         # where they are, and whether they match this binary
```

Installing puts the skills where every harness looks; loading them is
still the model's call, made from a one-line description. `taskr skill
enforce` closes that gap: it plants a short session-start directive in
each harness — a `SessionStart` hook in `~/.claude/settings.json` that
runs `taskr skill nudge`, a marked block in `~/.codex/AGENTS.md` and
`~/.config/opencode/AGENTS.md`, and an `alwaysApply` rule at
`.cursor/rules/taskr.mdc` in the repository you run it from. Rerunning
it is a no-op; the marked blocks are rewritten in place on upgrade, and
everything else in those files is left alone.

The skills ship inside the binary, so `taskr skill install` after an
upgrade rewrites them in step with the verbs the binary actually has.
`go install` does not run it for you — run it once by hand.

Windows: grab `taskr_<version>_windows_amd64.zip` from the
[releases page](https://github.com/thomas-cabral/taskr-cli/releases).

Or build from a checkout:

```bash
go build -o bin/taskr ./cmd/taskr
```

## Authenticate

```bash
taskr auth login
```

Prints a verification URL and short code; approve it in a browser.
You can also pipe a key for CI and containers: `echo $TASKR_KEY | taskr auth login`.

## Usage

The daily loop:

```bash
taskr context                 # where am I, what was I doing
taskr next                    # ranked candidates
taskr start TSK-42             # start or resume work; prints the resume packet
taskr comment TSK-42 -m "..."  # leave a note as you go
taskr close TSK-42 -r "..."    # done
```

The full command reference:

```bash
taskr context                 # where am I, what was I doing
taskr next                    # ranked candidates
taskr ls [-s status] [-q q]   # list issues
taskr show <ref> [--context]  # issue detail
taskr new <title> [-k kind] [-p priority] [-m description]
taskr start <ref>             # start or resume work; prints the resume packet
taskr park -m <note> [-r reason]
taskr end [-r reason]
taskr close <ref> [-r resolution]
taskr offload <title> -m <brief> [-k kind] [-s severity]
taskr comment <ref> -m <text>
taskr triage <ref> <verdict> [-e evidence] [-d duplicate-of]
taskr timeline <ref>
taskr doc <ref>
taskr auth login               # prints a code, approve it in your browser
echo $TASKR_KEY | taskr auth login   # or pipe a key, for CI and containers
taskr auth status
taskr version
taskr project ls               # projects, their repos, dirs and conventions
taskr project init <slug> --key KEY [--branch-format F] [--pr-target BRANCH]
```

A project's conventions — the branch-name shape, the commit style, the
branch PRs are opened against — are printed by `project ls` and by `taskr
context` beside the project it resolved, so an agent reads them instead of
guessing. `project init` sets them, on a new project or an existing one; a
convention you do not name is left exactly as it was.

Every command accepts `--json` for machine-readable output; the default is
human-readable prose, which matters most for `taskr start` — the resume
packet is the product, not a data dump. Run `taskr help` for the full
command reference.

`new` and `offload` answer with any open issue that already says the same
thing — `Similar open issues:`, scored, matched by meaning rather than
keyword — so a twin is caught before it becomes two records. The same block
rides on `show --context`, `triage <ref>` lists the twins the `duplicate`
verdict needs, and the bare `triage` queue marks pairs in a `TWIN` column.
Suggestions only: nothing is blocked, and closed or already-linked issues
are never offered.

## Checks

A check is a done-when — a constraint that gates `taskr close`. Register
checks to enforce preconditions before closing (e.g., test coverage, load
benchmarks, manual review). taskr never runs the procedure itself: someone
carries it out by hand, then calls `check run` to record what happened.

```bash
taskr check add <ref> -m <procedure> [--expect <text>] [--human]
taskr check ls <ref>
taskr check run <id> --pass|--fail [--measure metric=value[unit]]
taskr close <ref> [--despite-checks]
```

Example:

```bash
taskr check add TSK-9 -m "hey -z 30s -c 50 GET /api/issues" --expect "> 100 r/s" --human
```

- `taskr check add <ref> -m <procedure> --expect <expectation>` — register a
  check. `--human` sets its runner to human instead of agent, naming who
  should carry out the procedure — not who runs `check run`.
- `taskr check ls <ref>` — list an issue's checks, with the id `check run`
  needs.
- `taskr check run <id> --pass|--fail` — record a result against a check id
  (from `check add`'s output or `check ls`).
- `taskr close <ref> --despite-checks` — close even though checks are
  pending; each pending check is recorded as skipped.

## Configuration

Config lives at `$XDG_CONFIG_HOME/taskr/hosts.json` (falling back to
`~/.config`), keyed by host, so you can stay authenticated to a local
instance and a hosted one at once.

Environment variables:

- `TASKR_API` — selects the host (default `https://api.aitaskr.com`; point
  it at a self-hosted instance, or at `127.0.0.1:8099` for a local one)
- `TASKR_KEY` — overrides whatever is stored for the selected host
- `TASKR_SESSION` — names this invocation context, so two terminals (or a
  terminal and an agent) on one machine do not share a work session.
  Defaults to a session id the harness published (`CLAUDE_CODE_SESSION_ID`,
  `OPENCODE_PID`), and failing that to the parent process id. Export it
  yourself under a harness that publishes neither and spawns a fresh shell
  per tool call, where the parent process id changes on every command.
- `TASKR_REMOTE`, `TASKR_ROOT`, `TASKR_HEAD`, `TASKR_BRANCH` — **you do not
  need to export these.** `taskr` reads them out of `.git` — `config` for
  the origin remote, `HEAD` and the refs (loose or packed) for the commit
  and branch, following `commondir` in a linked worktree — which is what
  resolves your project, scopes `new`, `offload`, `next` and `ls` to it,
  and keeps rot detection fed. It reads git's files; it does not run git,
  and does not need it installed. Export one only to override it, e.g. to
  file work against a repo you are not standing in.
- `TASKR_MERGE_BASE`, `TASKR_DIRTY` — the two parts of the tree state
  `taskr` cannot read for itself, since a merge base needs a commit-graph
  walk and a dirty list needs the index diffed against the worktree:

  ```sh
  export TASKR_MERGE_BASE=$(git merge-base HEAD origin/HEAD)
  export TASKR_DIRTY=$(git status --porcelain | cut -c4-)   # one path per line
  ```

  A head is the gate: with no commit to anchor it — nothing exported and no
  checkout to read — no snapshot is sent at all, because a block of blank
  fields is worse than an honest "no git snapshot has been recorded". The
  others are best-effort; a detached HEAD has no branch and records as
  `(detached)` rather than costing you the snapshot.

## Stability

This module's only supported exported symbol is `cli.Run`. Everything
else — package layout, unexported behavior, and any type not part of
`Run`'s signature — may change without a major version bump. Depend on
`Run` if you need to drive the CLI in-process (this is how `taskr`'s own
integration tests do it); treat anything else in the `cli` package as an
implementation detail that can move under you.

## License

Apache-2.0. See [LICENSE](LICENSE).
