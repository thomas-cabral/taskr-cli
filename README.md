# taskr-cli

`taskr` is the command-line client for a [taskr](https://github.com/thomas-cabral/taskr)
server — issues, specs and plans that survive losing context, whether the
caller is a person or an agent shelling out between turns.

It is an HTTP client and nothing else: it holds no domain logic, never
opens a database, and never shells out to git — git state (remote, HEAD) is
something a caller reports, not something `taskr` goes and gets. This repo
builds only the `taskr` binary; it does not include the server.

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

Reads the key from stdin, never argv — it never lands in shell history or
a process listing.

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
taskr auth login               # reads the key from stdin, never argv
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
  terminal and an agent) on one machine do not share a work session;
  defaults to the parent process id
- `TASKR_REMOTE`, `TASKR_ROOT`, `TASKR_HEAD` — the output of
  `git remote get-url origin`, `git rev-parse --show-toplevel` and
  `git rev-parse HEAD`. `taskr` never runs git; exporting these lets it
  resolve your project from the repo and directory you are in, keep rot
  detection fed, and scope `new`, `offload`, `next` and `ls` to it.
- `TASKR_BRANCH`, `TASKR_MERGE_BASE`, `TASKR_DIRTY` — the rest of the tree
  state `new`, `offload` and `park` record on an issue, so `taskr start`
  can tell the next reader where the work lives:

  ```sh
  export TASKR_BRANCH=$(git branch --show-current)
  export TASKR_MERGE_BASE=$(git merge-base HEAD origin/HEAD)
  export TASKR_DIRTY=$(git status --porcelain | cut -c4-)   # one path per line
  ```

  `TASKR_HEAD` is the gate: with no commit to anchor it, no snapshot is
  sent at all, because a block of blank fields is worse than an honest
  "no git snapshot has been recorded". The others are best-effort — a
  detached HEAD has no branch and records as `(detached)` rather than
  costing you the snapshot.

## Stability

This module's only supported exported symbol is `cli.Run`. Everything
else — package layout, unexported behavior, and any type not part of
`Run`'s signature — may change without a major version bump. Depend on
`Run` if you need to drive the CLI in-process (this is how `taskr`'s own
integration tests do it); treat anything else in the `cli` package as an
implementation detail that can move under you.

## License

Apache-2.0. See [LICENSE](LICENSE).
