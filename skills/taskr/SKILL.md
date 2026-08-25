---
name: taskr
description: Use when starting work, picking up where you left off, or discovering work mid-task that should not derail the current one. Tracks issues, specs and plans with a resume packet that survives losing context.
---

# taskr

taskr is a running-context system: issues, specs, and work sessions that
survive you losing context — a compaction, a crash, a new agent picking up
where you left off. `taskr` is the CLI; pass `--json` on any command for
parseable output — the default is human-readable.

## The loop

1. **Orient** — `taskr context` or `taskr next`: where am I, what's ready.
2. **Pick** — `taskr show <ref>` to read it, `taskr start <ref>` to begin.
3. **Work** — do the task, keeping the plan on the issue with `taskr step`
   so it survives you, not just the session.
4. **Offload** what you find — `taskr offload` — without derailing.
5. **Attach** anything durable you wrote — a spec, a plan — to its issue.
6. **Park** with a note — `taskr park -m "<next action>"` — before you stop.
7. **Close** it when the work is actually done — `taskr close <ref>`.

## When to reach for it

- **Before starting any task**, run `taskr context`. It reports whether a
  session is already in progress and what it was doing.
- **Before picking your own work**, run `taskr next` instead of guessing —
  it's a ranked, triaged queue, not a wishlist. Triage gates it, so a
  freshly imported project can have plenty of open work and an empty
  queue; `next` says so when that is why, and `--untriaged` ranks it
  anyway.
- **When you notice something wrong that isn't what you're working on** — a
  bug, a missing test, a bad assumption in another file — `taskr offload`
  it immediately. Do not fix it inline (scope creep) and do not just
  mention it in your final message and move on (that evaporates the moment
  the conversation ends). Offloading is nearly free; losing the thread
  isn't.
- **The moment you write a spec, a plan, or any document meant to outlive
  the session**, attach it to its issue — before you keep working, not at
  the end. A spec that exists only as a file in git is invisible to
  everyone working through taskr: `taskr show` does not surface it and
  `taskr next` cannot rank by it, so the next agent re-derives a design
  that was already settled and paid for. Committing it is not attaching
  it — `taskr doc add <ref> -f <path>` is.
- **When a done-when cannot be verified yet** — it needs a deploy, a
  production run, or an action only a human may take (a switchover, a
  restart) — record it as a check the moment you know it:
  `taskr check add <ref> -m "<the exact command or steps>" --expect
  "<what passing looks like>" [--human]`. `taskr close` refuses while a
  check is pending (it lists them; `--despite-checks` overrides, on the
  record), so a checked issue can never read as done-but-unverified.
  When you run one, record the result with typed numbers, not prose:
  `taskr check run <id> --pass --measure list.p50=0.057s --conditions
  "c50, 10 keys" [--sha <head>] [-e <doc-id>]`. `taskr next` prints
  pending human-run checks as their own block — that is how a person
  finds what only they can move.
- **Before you stop for any reason** — done, blocked, interrupted, out of
  context, or handing off — `taskr park -m "<note>"`. A session that ends
  without parking leaves the next reader nothing to resume from.
- **When an issue is fully done**, `taskr close <ref> -r "<how it ended>"`.
  Three verbs sound alike here and do different things: `close` finishes the
  ISSUE, `end` closes the work SESSION and leaves the issue where it is, and
  `triage` records whether a report was real — which is not the same question
  as whether the work is finished. Closing does not end a live session on the
  issue; it says so and leaves `taskr end` to you.

## How to write a brief that survives

This is the part no tool schema enforces for you. `taskr offload` and
`taskr new` both take a `-m` description that gets read cold, by an agent
with none of your context, possibly days later. It needs four things:
**what's wrong, why it matters, where it lives, and how to tell when it's
done.** "Where it lives" means a `file:line`, not a vague area. If you
ruled something out, say so — otherwise the next agent re-runs your dead
end.

**Bad:**
> "Login is broken sometimes, might be a race condition, someone should
> look into it."

Unactionable: no file, no repro, no definition of done. The next agent
starts from zero — worse than zero, since "might be a race condition" now
has to be distinguished from an actual finding.

**Good:**
> "Login silently no-ops when a session cookie is set but X-Taskr-Key is
> present-and-empty — internal/api/auth.go:104, authenticated() checks the
> key header first and never falls through to the cookie once that branch
> is taken. Repro: log in via the SPA, then call any endpoint with that
> session's cookie plus an empty X-Taskr-Key header (not absent). Ruled
> out: not cookie expiry, timestamps are fresh. Done when a test alongside
> TestValidKeyHeaderPasses covers the empty-but-present header case and
> passes."

## How to attach a document

```bash
taskr doc add TSK-24 -f docs/superpowers/specs/2026-08-18-my-design.md -t spec
taskr doc TSK-24          # confirm it is listed
taskr doc show <id>       # read one back
```

`-t` is `spec`, `plan` or `note` and defaults to `spec`. The title comes from
the body's first `# ` heading, then the file name; `--title` overrides both.
`-f -` reads the body from stdin. Revise an attached document in place with
`--id <id> --diff "<what changed>"` rather than attaching a second copy.

The **body** is sent, not the path — a reader on another machine has your
issue and not your checkout. Committing a spec to git is not attaching it:
`taskr show` does not surface documents and `taskr next` cannot rank by one,
so a spec that lives only in your checkout is invisible to everyone else
working through taskr, and the next agent re-derives a design that was
already settled and paid for.

**Do not put an `actor` in the body if you hand-roll a request.** Authorship
comes from the key in `TASKR_KEY`, not from the request: whatever a body
claims is ignored for any authenticated caller.

Check which name your writes will carry *before* you make one: `taskr auth
status` reports it, and `taskr context` prints it as `writes as:` while you
orient. If it says `user` and you are an agent, the key is labelled wrong,
not the request — `auth status` prints the exact relabel command, which your
human partner runs: `taskr-admin key actor <id> agent` (TSK-26, TSK-38).
Noticed at orientation this costs nothing; noticed afterwards it means a
ledger with the wrong name on every write you made.

## How to park

The resume note is the first thing the next agent — possibly you, later —
reads. **Name the next concrete action.** Do not summarize what happened;
`taskr timeline <ref>` already has that, event by event.

- Bad: `-m "Worked on the auth bug, made some progress."`
- Good: `-m "Add the empty-header-but-present-cookie case to auth_test.go,
  next to TestValidKeyHeaderPasses; the fix in authenticated() is done."`

Always pass `-r <reason>`: `done_for_now`, `blocked`, `interrupted`,
`context_exhausted`, or `handoff`. It's how `taskr context` and `taskr
next` tell "pick this back up any time" apart from "actually stuck."

## Command reference

Every command below also accepts `--json`.

| Command | Does |
|---|---|
| `taskr context` | where am I, what was I doing |
| `taskr next [--untriaged]` | ranked ready queue; only issues with an actionable verdict unless `--untriaged` |
| `taskr ls [-s status] [-q query]` | list/search issues (`-s` repeatable) |
| `taskr show <ref> [--context]` | issue detail; `--context` adds agent notes |
| `taskr new <title> [-k kind] [-p priority] [-m desc] [--parent GROUP]` | open an issue |
| `taskr group add <group> <child>` | add an existing issue to a group |
| `taskr group rm <group> <child>` | take an issue out of a group |
| `taskr start <ref>` | begin/resume work; prints the resume packet |
| `taskr park -m <note> [-r reason]` | stop, naming the next action |
| `taskr end [-r reason]` | close the current session (not the issue) |
| `taskr close <ref> [-r resolution]` | finish the issue; the session stays open |
| `taskr offload <title> -m <brief> [-k kind] [-s severity]` | file discovered work |
| `taskr comment <ref> -m <text>` | add a comment |
| `taskr triage <ref> <verdict> [-e evidence] [-d dup-ref]` | record a verdict |
| `taskr timeline <ref>` | the event ledger |
| `taskr check add <ref> -m <procedure> [--expect T] [--human]` | record a done-when that cannot be verified yet |
| `taskr check ls <ref>` | an issue's checks and their state |
| `taskr check run <id> --pass\|--fail [--measure m=v]` | record a result |
| `taskr step ls <ref>` | the issue's ordered working plan |
| `taskr step add <ref> "title" ["title" ...] [--after <pos\|id>] [--body T]` | add steps to the plan |
| `taskr step start\|done <ref> <pos\|id> [-m note]` | move a step |
| `taskr step mv <ref> <pos\|id> --after <pos\|id>\|--front` | reorder |
| `taskr step edit <ref> <pos\|id> [--title T] [--body T]` | reword a step |
| `taskr step drop <ref> <pos\|id> -m <reason>` | drop a step, on the record |
| `taskr step promote <ref> <pos\|id> [--child\|--check] [--no-block]` | a step that turned out to be its own issue |
| `taskr relate <ref> <type> <target>` | record a dependency: BLOCKS, BLOCKED_BY, RELATES_TO, DUPLICATE_OF, DISCOVERED_DURING, DISCOVERED |
| `taskr unrelate <ref> <type> <target>` | remove one |
| `taskr doc <ref>` | list documents linked to an issue |
| `taskr doc add <ref> -f <path> [-t type] [--title T]` | attach a spec, plan or note |
| `taskr doc show <id>` | print one document's body |
| `taskr project ls` | every registered project, with its repos and dirs |
| `taskr project init <slug> --key KEY` | create a project (see the taskr-onboarding skill) |
| `taskr project attach [--project S] [--repo URL] [--dir SUBPATH]` | register a repo, or a dir inside one |
| `taskr auth login` | prints a code to approve in a browser; a piped key still works |
| `taskr auth status` | who your credential writes as, without writing anything |
| `taskr skill install [--dir D] [--dry-run]` | write these skills where your harness reads them |
| `taskr version` | which commit this binary was built from, and whether it matches your checkout |

`kind`: bug, feature, task, chore, spike, question, group.
`priority`: critical, high, medium, low.
`verdict`: actionable, already_fixed, duplicate, stale, needs_info.

`new` and `offload` take `--project <slug>` to name a project outright;
`next` and `ls` take `--all` to widen past the project you're standing in.

## Plans live on the issue

An issue's steps are its working plan, and they are the part a resume packet
can actually carry — a plan in your head or in a scratch file does not
survive the session that wrote it. `taskr step add` when you know the shape
of the work, `step start`/`step done` as you move, `step drop -m` when a
step turns out to be wrong (say why: the next reader needs to know it was
considered, not forgotten). When a step is really its own issue, `step
promote` makes it one rather than leaving a plan item nobody can rank.

**A step is what you can finish before you stop.** That is the whole rule,
and it is what keeps a plan from turning into a second issue tracker inside
an issue. Work that spans pull requests, spans repositories, or can only be
verified after something lands is not a step: it is a child issue, or it is
a check. `taskr step promote <ref> <pos> --child` makes it the first and
wires a `BLOCKS` edge so this issue leaves the ready queue until it lands;
`--check` makes it the second, and the close gate holds instead. Reaching
for promote is the plan working, not the plan failing — a step that quietly
grew into a week of work is how a plan stops being true.

**Starting and finishing a step records the commit you were at.** That is
the point of moving them rather than only writing them: `taskr context`
tells the next agent "step 3 of 6, in progress since `abc123`", so picking
the work up lands somewhere real instead of somewhere described. Add `-m`
when the commit alone does not say what happened — `step done <ref> 3 -m
"fallback lands in auth.go:112"` costs nothing and saves a hunt.

**Closing an issue abandons whatever the plan did not reach, and tells
you.** Steps never block a close — a plan is not a promise — but `taskr
close` hands back every unfinished step rather than swallowing it. Do
something with that list: name it in the resolution if it turned out not to
matter, or `taskr offload` it if it did. A step abandoned in silence is
exactly the thread this tool exists to stop losing.

Record the sequencing while the work is live. `taskr relate <ref> BLOCKS
<other>` refuses a closed issue, so an ordering you leave until afterwards
cannot be written down at all.

## Where taskr thinks you are

**You do not have to export anything.** taskr reads the checkout you are
standing in out of `.git` itself — `config` for the origin remote, `HEAD`
and the refs for the commit and branch, following `commondir` when you are
in a `git worktree`. That is where your project, your subpath in a
monorepo, and the tree state on a new issue come from. Nothing is spawned:
it reads git's files, it does not run git, and a machine with no `git` on
PATH resolves a project the same way.

Two values it cannot read that way, because answering them needs git's own
machinery rather than a file: the merge base (a commit-graph walk) and the
dirty list (the index diffed against the worktree). Supply those yourself
when they matter to the next reader — they enrich the snapshot `new`,
`offload` and `park` record:

```bash
export TASKR_MERGE_BASE=$(git merge-base HEAD origin/HEAD)
export TASKR_DIRTY=$(git status --porcelain | cut -c4-)
```

Every discovered value has an override, and an exported one always wins:
`TASKR_REMOTE`, `TASKR_ROOT`, `TASKR_HEAD`, `TASKR_BRANCH`. Reach for them
to file work against a repo you are not standing in — not as routine setup.

`TASKR_SESSION` names which invocation context you are, so two terminals —
or a terminal and an agent — don't share one work session. If your harness
publishes a session id (`CLAUDE_CODE_SESSION_ID`, `OPENCODE_PID`) taskr
uses it without being told; otherwise it falls back to the parent process
id. Export `TASKR_SESSION` yourself if you shell out from a harness that
publishes neither and your session keeps resetting between calls.

`taskr version` tells you whether the binary you are running is the code in
your checkout — it compares the commit it was built from against the head
it just read. A `taskr` built by hand silently runs old code between a
commit and the next build: a fixed bug reads as still broken, and a shipped
flag reads as "flag provided but not defined". `taskr context` warns when
it can prove the mismatch, and stays quiet from any repo that is not
taskr's own. If it warns, rebuild with `go build -o bin/taskr ./cmd/taskr`
before trusting any CLI observation as evidence.

## Keeping the skill current

This file ships inside the `taskr` binary. `taskr skill install` writes it
to `~/.agents/skills/taskr/` and `~/.claude/skills/taskr/`, which is every
directory Claude Code, Codex, Cursor and opencode read — the installer runs
it for you. `taskr skill ls` reports whether the copy on disk still matches
the binary; `modified` means an upgraded taskr is describing verbs your
installed skill has never heard of, and `taskr skill install` fixes it.

## What taskr does not do

taskr never runs `git` or `gh`, and never opens a database directly — the
CLI is an HTTP client that reads a few of git's files and nothing else. It
does not collect anything it cannot read: **a merge base, a dirty tree, or
any other state that needs git to compute has to come from you.** If it
matters to the next reader and you have not exported it, put it in your
brief or resume note rather than assuming the packet carries it.

taskr also doesn't manage PRs, run CI, or replace GitHub Issues — it's a
memory layer for you and the agents working alongside you, not a
project-management tool.
