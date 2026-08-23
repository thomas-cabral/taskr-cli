# taskr-cli

`taskr` is the command-line client for a [taskr](https://github.com/thomas-cabral/taskr)
server — issues, specs and plans that survive losing context, whether the
caller is a person or an agent shelling out between turns.

It is an HTTP client and nothing else: it holds no domain logic, never
opens a database, and never shells out to git — git state (remote, HEAD) is
something a caller reports, not something `taskr` goes and gets. This repo
builds only the `taskr` binary; it does not include the server.

## Install

macOS and Linux:

```bash
curl -fsSL https://aitaskr.com/install.sh | sh
```

This puts the binary in `~/.local/bin`; set `TASKR_INSTALL_DIR` to install
somewhere else. With a Go toolchain instead:

```bash
go install github.com/thomas-cabral/taskr-cli/cmd/taskr@latest
```

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
taskr project ls
```

Every command accepts `--json` for machine-readable output; the default is
human-readable prose, which matters most for `taskr start` — the resume
packet is the product, not a data dump. Run `taskr help` for the full
command reference.

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

## Stability

This module's only supported exported symbol is `cli.Run`. Everything
else — package layout, unexported behavior, and any type not part of
`Run`'s signature — may change without a major version bump. Depend on
`Run` if you need to drive the CLI in-process (this is how `taskr`'s own
integration tests do it); treat anything else in the `cli` package as an
implementation detail that can move under you.

## License

Apache-2.0. See [LICENSE](LICENSE).
