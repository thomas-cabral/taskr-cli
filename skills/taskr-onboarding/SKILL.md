---
name: taskr-onboarding
description: Use when a taskr write is refused because the repo is not registered, or `taskr context` reports a setup hint. Registers a repo — and, in a monorepo, a directory inside it — to a taskr project, and proves the registration actually took before you move on.
---

# taskr-onboarding

taskr refuses a write from a repo it has never seen rather than filing it
into whatever project happened to be active. This skill is how you clear
that refusal: register the repo (and, if it shares a checkout with other
products, the directory) to a project, then prove the registration took.
It is not part of the daily loop — most sessions never touch it. It exists
for the one time a write bounces.

## 1. When to use it

You land here one of two ways:

- A write — `taskr new`, `taskr offload` — failed with an error containing
  "repo is not registered." The error names this skill because the fix is
  a handful of commands, not a person you have to go track down.
- `taskr context` returned a setup hint instead of a project: the same
  situation, surfaced before you tried to write anything.

Either way, stop and register before retrying with a guessed `--project`.
Guessing to force the write through is exactly the failure this refusal
exists to prevent — see the taskr skill's "Where taskr thinks you are" for
how a registered repo gets routed once this is done.

## 2. Collect

taskr already knows which repo you are standing in: it reads the origin
remote and the worktree root out of `.git` itself. What it cannot know is
anything about the repo as a *product*, so collect that yourself:

```bash
git remote get-url origin   # the value step 5 passes to --repo
gh repo view --json defaultBranchRef,url,owner,name
hostname
```

Having the remote in front of you matters because step 5 passes it
explicitly — this early, being explicit beats trusting a default you have
not yet watched work. `gh repo view` and `hostname` are for the note about
the registration itself: taskr doesn't need them to register a repo, but
the caller who asks "why did this get onboarded" later will.

## 3. Decide: new project, or joining one that exists?

Run `taskr project ls` before creating anything. It lists every project
alongside the repos and directories already attached — read it before
deciding there's nothing here to join.

The question that's easy to get backwards:

- **A product that spans several repos — frontend, backend, infra — is
  ONE project with several repos attached.** Attach each repo to the same
  slug. Splitting them into separate projects scatters one backlog across
  several: `taskr next` in any one repo only ever sees a fraction of the
  work.
- **A monorepo hosting several unrelated products is SEVERAL projects,
  distinguished by directory** — not one project for the whole repo.
  Attach the same repo more than once, each with a different `--dir`, so a
  write from `apps/web` routes to a different project than one from
  `apps/api`. Registering the repo once with no directory only works if
  the whole repo really is one product; the moment a second directory
  wants its own backlog, an undirected registration makes every write
  from either directory ambiguous.

If `taskr project ls` already shows a project whose repo (and, for a
monorepo, whose directory) covers where you're standing, skip step 4 —
the key is already fixed — and go straight to step 5 to attach.

## 4. Choose a key

Skip this step if you're attaching to a project that already exists; its
key was decided when it was created and you just read it off `taskr
project ls`. This step is only for a genuinely new project.

Pick 2-5 uppercase letters. This is the one choice in the whole flow you
cannot revisit: it becomes the prefix of every issue ref this project ever
creates — `DEMO-1`, `DEMO-2`, forever. `taskr project rename` can change
the slug later; nothing changes the key. Get it wrong and every future ref
carries the mistake permanently.

Keys are unique across every project taskr knows about, not just the ones
touching this repo. `taskr project init` checks on creation and, if
another project already holds the key you asked for, reports which slug
holds it — read that error and pick a different key rather than retrying
the same one.

## 5. Register

New project:

```bash
taskr project init <slug> --key <KEY>
taskr project attach --project <slug> --repo "$(git remote get-url origin)"
```

Joining a project `taskr project ls` already showed you:

```bash
taskr project attach --project <slug> --repo "$(git remote get-url origin)"
```

Add `--dir <subpath>` to either form when this repo is a monorepo and
you're registering one product's directory rather than the whole repo —
`<subpath>` is repo-relative, e.g. `apps/web`, matching what you decided
in step 3. Leave it off to claim the whole repo.

`--project` and `--repo` both have defaults — the project your locator
resolves to, and the origin remote taskr read out of `.git`. Pass them
explicitly anyway: you are here precisely because resolution is not working
yet, and a registration aimed at whatever the default resolved to is the
one mistake this whole skill exists to avoid.

## 6. Verify — this is not optional

An unverified registration looks identical to no registration at all: both
leave you looking at a normal prompt. The only difference shows up the
next time something writes — and by then you've lost the thread on why it
failed. Prove it took before calling this done:

```bash
taskr context                          # must name the project you just registered
taskr new "onboarding check" -k chore  # must print a ref, not a refusal
taskr close <the-ref-it-printed> -r "onboarding verified"
```

Run both from inside the repo you just registered — that is what taskr
reads to decide where you are.

Close the check issue in the same breath. It has served its purpose the
moment it printed a ref, and an open chore nobody intends to do is the
first thing in a brand-new project's `taskr next`.

If `taskr context` still reports a setup hint, or `taskr new` still
refuses, the registration didn't take. Recheck that the remote you
attached is the same repo as `git remote get-url origin` — taskr
normalizes scheme (`git@`/`https://`/`ssh://`) and a trailing `.git`, but
a different host, owner, or repo name will not match — then redo step 5.
Don't move on until both commands above succeed.

## 7. Nothing to export

There is no environment to set up afterwards. Routing by repo and by
directory works from the checkout itself, for this session and every one
after it, on every machine that registers the same repo.

You're done — return to whatever `taskr new` or `taskr offload` call got
refused, and it will land.
