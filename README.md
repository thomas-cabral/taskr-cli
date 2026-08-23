# taskr-cli

`taskr` is the command-line client for a [taskr](https://github.com/thomas-cabral/taskr)
server — issues, specs and plans that survive losing context, whether the
caller is a person or an agent shelling out between turns.

It is an HTTP client and nothing else: it holds no domain logic, never
opens a database, and never shells out to git — git state (remote, HEAD) is
something a caller reports, not something `taskr` goes and gets. This repo
builds only the `taskr` binary; it does not include the server.

## Install

```bash
go install github.com/thomas-cabral/taskr-cli/cmd/taskr@latest
```

Or build from a checkout:

```bash
go build -o bin/taskr ./cmd/taskr
```

## Usage

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

- `TASKR_API` — selects the host (default `http://127.0.0.1:8099`)
- `TASKR_KEY` — overrides whatever is stored for the selected host
- `TASKR_SESSION` — names this invocation context, so two terminals (or a
  terminal and an agent) on one machine do not share a work session;
  defaults to the parent process id
- `TASKR_REMOTE`, `TASKR_ROOT`, `TASKR_HEAD` — the output of
  `git remote get-url origin`, `git rev-parse --show-toplevel` and
  `git rev-parse HEAD`. `taskr` never runs git; exporting these lets it
  resolve your project from the repo and directory you are in, keep rot
  detection fed, and scope `new`, `offload`, `next` and `ls` to it.

## License

Apache-2.0. See [LICENSE](LICENSE).
