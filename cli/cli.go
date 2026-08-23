// cli/cli.go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const usage = `taskr — track issues, specs and plans across sessions.

Usage:
  taskr context                          where am I, what was I doing
  taskr next [--untriaged]               ranked candidates; only triaged ones unless --untriaged
  taskr ls [-s status] [-q query]        list issues
  taskr show <ref> [--context]           issue detail
  taskr new <title> [-k kind] [-p priority] [-m description] [--parent GROUP]
  taskr group add <group> <child>        add an existing issue to a group
  taskr group rm <group> <child>         take an issue out of a group
  taskr start <ref>                      start or resume work; prints the resume packet
  taskr park -m <note> [-r reason]       stop work, naming the next action
  taskr end [-r reason]                  close out the current session
  taskr close <ref> [-r resolution]      finish the ISSUE — end closes the session
  taskr offload <title> -m <brief> [-k kind] [-s severity]
  taskr comment <ref> -m <text>
  taskr triage <ref> <verdict> [-e evidence] [-d duplicate-of]
  taskr check add <ref> -m <procedure> [--expect <text>] [--human]
                                          record a done-when on an issue
  taskr check ls <ref>                   list an issue's checks
  taskr check run <id> --pass|--fail [--measure metric=value[unit]]
                                          record a result
  taskr timeline <ref>                   the event ledger
  taskr doc <ref>                        documents linked to an issue
  taskr doc add <ref> -f <path> [-t spec|plan|note] [--title T]
  taskr doc show <id>                    print one document's body
  taskr auth login                       read a key from stdin and store it
  taskr auth status                      who your credential writes as, without writing
  taskr version                          which commit this binary was built from
  taskr project ls                       every registered project, with its repos and dirs
  taskr project init <slug> --key KEY [--name N]
  taskr project attach [--project S] [--repo URL] [--dir SUBPATH]
  taskr project rename <slug> <new-slug> [--name N]

Every command accepts --json for machine-readable output.
new and offload accept --project <slug> to name a project outright.
next and ls accept --all to widen past the caller's resolved project.
Env: TASKR_API (default https://api.aitaskr.com), TASKR_KEY.
     TASKR_SESSION names this invocation context, so two terminals (or a
     terminal and an agent) on one machine do not share a work session.
     Defaults to the parent process id.
     TASKR_REMOTE, TASKR_ROOT and TASKR_HEAD are the output of
     "git remote get-url origin", "git rev-parse --show-toplevel" and
     "git rev-parse HEAD". taskr never runs git; export them and taskr
     resolves your project from the repo and directory you are in, keeps
     rot detection fed, and scopes new, offload, next and ls to it.
`

// agentCLI is the agent identifier every session this CLI opens is
// recorded under. It was domain.AgentCLI before the CLI became its own
// module; the value is a wire constant stored in existing rows and must
// not change.
const agentCLI = "taskr-cli"

// Run is the CLI's entire dispatch table. It is exported so cmd/taskr-cli's
// main can be a two-line wrapper, and so tests can drive it without a
// subprocess.
func Run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}

	cmd, rest := args[0], args[1:]

	// version answers before a target is resolved: a binary you suspect of
	// being stale is exactly the one that may be pointed at the wrong host
	// or hold no credential, and an answer that needed the API would be
	// missing in the case it is wanted for.
	if cmd == "version" || cmd == "--version" {
		if err := cmdVersion(rest, stdout, stderr, getenv); err != nil {
			fmt.Fprintln(stderr, "taskr:", err)
			return 1
		}
		return 0
	}

	if cmd == "auth" {
		if err := runAuth(rest, stdout, stderr, getenv); err != nil {
			fmt.Fprintln(stderr, "taskr:", err)
			return 1
		}
		return 0
	}

	target, err := resolveTarget(getenv, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "taskr:", err)
		return 1
	}
	client := &Client{BaseURL: target.BaseURL, Key: target.Key}
	ctx := context.Background()
	machine := machineName()
	session := agentSessionID(getenv)

	var run func() error
	switch cmd {
	case "context":
		run = func() error { return cmdContext(ctx, client, rest, stdout, stderr, machine, session, getenv) }
	case "next":
		run = func() error { return cmdNext(ctx, client, rest, stdout, stderr, machine, getenv) }
	case "ls":
		run = func() error { return cmdLs(ctx, client, rest, stdout, stderr, getenv) }
	case "show":
		run = func() error { return cmdShow(ctx, client, rest, stdout, stderr) }
	case "new":
		run = func() error { return cmdNew(ctx, client, rest, stdout, stderr, getenv) }
	case "start":
		run = func() error { return cmdStart(ctx, client, rest, stdout, stderr, machine, session) }
	case "park":
		run = func() error { return cmdPark(ctx, client, rest, stdout, stderr, machine, session) }
	case "end":
		run = func() error { return cmdEnd(ctx, client, rest, stdout, stderr, machine, session) }
	case "close":
		run = func() error { return cmdClose(ctx, client, rest, stdout, stderr, machine, session) }
	case "offload":
		run = func() error { return cmdOffload(ctx, client, rest, stdout, stderr, machine, session, getenv) }
	case "comment":
		run = func() error { return cmdComment(ctx, client, rest, stdout, stderr) }
	case "triage":
		run = func() error { return cmdTriage(ctx, client, rest, stdout, stderr) }
	case "timeline":
		run = func() error { return cmdTimeline(ctx, client, rest, stdout, stderr) }
	case "doc":
		run = func() error { return runDoc(ctx, client, rest, os.Stdin, stdout, stderr) }
	case "group":
		run = func() error { return runGroup(ctx, client, rest, stdout, stderr) }
	case "check":
		run = func() error { return runCheck(ctx, client, rest, stdout, stderr, getenv) }
	case "project":
		run = func() error { return runProject(ctx, client, rest, stdout, stderr, getenv) }
	default:
		fmt.Fprintf(stderr, "taskr: unknown command %q\n\n%s", cmd, usage)
		return 1
	}

	if err := run(); err != nil {
		fmt.Fprintln(stderr, "taskr:", err)
		return 1
	}
	return 0
}

// machineName is the one piece of "where am I" the CLI knows on its own.
// Anything git-shaped — branch, remote, dirty tree — is NOT: the caller
// reports that, because the CLI never shells out to git.
func machineName() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// agentSessionID identifies the invocation context this call belongs to.
//
// Work sessions are keyed by machine AND agent session, so naming the
// context is what gives this invocation a session of its own. Unnamed, it
// joins the pool of callers that named nothing and adopts whatever session
// is live there: the CLI would reuse a running MCP agent's session and emit
// FocusSwitched onto it, after which that agent's next park_work filed its
// resume note against the wrong issue.
//
// TASKR_SESSION wins when set, so an agent that already knows its own
// session id can export it and have the CLI it shells out to land in the
// same session rather than open a second one. Otherwise the parent process id
// stands in: stable for the life of one shell, and different between two
// terminals on the same machine — which is exactly the distinction that
// was missing.
func agentSessionID(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("TASKR_SESSION")); v != "" {
		return v
	}
	return fmt.Sprintf("ppid-%d", os.Getppid())
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

// okResult is what a write with no other response body reports under
// --json — park, end, comment and triage all answer 204 on success.
type okResult struct {
	OK bool `json:"ok"`
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// stringList collects a repeatable flag, e.g. `-s open -s parked`.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// parseFlags reorders args so recognized flags for fs can appear anywhere,
// including after positional arguments. Several taskr commands put a flag
// after a title or ref (`taskr new <title> -k bug`), which flag.FlagSet
// alone does not support: it stops parsing flags at the first non-flag
// token. This restores the natural word order without giving up flag.Value
// or usage generation.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flagArgs = append(flagArgs, a)
			name := strings.TrimLeft(a, "-")
			if strings.ContainsRune(name, '=') {
				continue // -flag=value is self-contained
			}
			if f := fs.Lookup(name); f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					continue // no separate value token to consume
				}
			}
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	if err := fs.Parse(append(flagArgs, positional...)); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

// ErrNoActiveSession reports that this machine has no session for an
// implicit session reference to resolve against. offload treats it as a
// missing link and files anyway; park and end treat it as fatal, since
// neither has anything to act on without a session.
var ErrNoActiveSession = errors.New("no active session")

// currentSession resolves the session an implicit reference means — used by
// park, end and offload, none of which take a session id on the command
// line. The active session on this machine wins; when allowParked is set
// and there is none, the most recently parked session on this machine is
// used instead. end can finish a session that is already parked, and an
// offload can be filed against one too — only park requires the session to
// still be active, since parking an already-parked session is itself an
// error.
func currentSession(ctx context.Context, c *Client, machine, agentSession string, allowParked bool) (SessionView, IssueView, error) {
	v, err := c.Context(ctx, ContextQuery{Machine: machine, AgentSessionID: agentSession})
	if err != nil {
		return SessionView{}, IssueView{}, err
	}
	if v.ActiveSession != nil {
		var iv IssueView
		if v.ActiveIssue != nil {
			iv = *v.ActiveIssue
		}
		return *v.ActiveSession, iv, nil
	}
	if allowParked {
		for _, s := range v.Parked {
			if s.Machine == machine {
				return s, IssueView{}, nil
			}
		}
	}
	return SessionView{}, IssueView{}, fmt.Errorf("%w on %s — run `taskr start <ref>` first", ErrNoActiveSession, machine)
}

func cmdContext(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine, agentSession string, getenv func(string) string) error {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	// Read, never collected. TASKR_REMOTE, TASKR_ROOT and TASKR_HEAD are how
	// a caller that has already run git hands the answers over; the CLI
	// does not run git itself, and omits whichever are unset. The locator
	// (remote + subpath) is built the same way `new`, `offload`, `next` and
	// `ls` build theirs, so a context call from a monorepo directory
	// resolves through the same path a write from there would.
	loc := LocatorFrom(getenv, cwd())
	v, err := c.Context(ctx, ContextQuery{
		Machine: machine, AgentSessionID: agentSession, CWD: cwd(),
		RemoteURL: loc.RemoteURL, Subpath: loc.Subpath,
		HeadSHA: strings.TrimSpace(getenv("TASKR_HEAD")),
	})
	if err != nil {
		return err
	}
	// Orientation is the one place a stale binary has to speak unasked: it
	// is where a session starts, and every CLI observation made afterwards
	// is only as trustworthy as the code that produced it. On stderr, so a
	// --json caller's stdout stays parseable.
	if w := stalenessFor(getenv); w != "" {
		fmt.Fprintln(stderr, "taskr:", w)
	}
	if *jsonOut {
		return printJSON(stdout, v)
	}
	// Who this credential writes as belongs at orientation, where it is
	// still cheap to fix: authorship comes from the key, and a key
	// mislabelled as the user is otherwise discovered only by reading a
	// write back and noticing the wrong name on it. A second request rather
	// than a field on the context view, because the context view is the
	// app layer's and knows nothing about credentials — and a failure here
	// is silent, because the actor is a nicety on top of the answer, not
	// the answer.
	actor := ""
	if st, err := c.AuthStatus(ctx); err == nil {
		actor = st.Actor
	}
	fmt.Fprint(stdout, RenderContext(v, actor))
	return nil
}

func cmdNext(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine string, getenv func(string) string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "every project, not just the one resolved from where you're standing")
	untriaged := fs.Bool("untriaged", false, "include issues with no actionable verdict, ranked below the triaged ones")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	loc := LocatorFrom(getenv, cwd())
	rows, err := c.Next(ctx, machine, loc, *all, *untriaged)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, rows)
	}
	// An empty queue is ambiguous in a way that matters: no issues, all of
	// them done, all blocked, and "forty filed but none triaged" all print
	// as the same nothing. Only the last one is a false negative, and it is
	// the one that happens right after an import — so ask what the pool
	// looks like without the triage gate and say so.
	if len(rows) == 0 && !*untriaged {
		// No count: the pool comes back capped at the same limit the real
		// queue uses, so any number printed here would be the cap rather
		// than the census, and a wrong number is worse than none.
		if pool, err := c.Next(ctx, machine, loc, *all, true); err == nil && len(pool) > 0 {
			fmt.Fprintln(stdout,
				"No triaged candidates, but this project has ready work with no triage verdict.\n"+
					"`taskr next --untriaged` ranks it anyway; `taskr triage <ref> actionable` promotes one into this queue.")
			return nil
		}
	}
	RenderCandidates(stdout, rows)
	return nil
}

func cmdLs(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var status stringList
	fs.Var(&status, "s", "filter by status (repeatable)")
	query := fs.String("q", "", "full text search")
	all := fs.Bool("all", false, "every project, not just the one resolved from where you're standing")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	rows, err := c.ListIssues(ctx, *query, status, LocatorFrom(getenv, cwd()), *all)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, rows)
	}
	RenderIssueTable(stdout, rows)
	return nil
}

func cmdShow(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	withContext := fs.Bool("context", false, "include the agent-context layer")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr show <ref> [--context]")
	}
	v, err := c.GetIssue(ctx, positional[0], *withContext)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, v)
	}
	fmt.Fprint(stdout, RenderIssue(v, *withContext))
	return nil
}

func cmdNew(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("k", "", "issue kind")
	priority := fs.String("p", "", "issue priority")
	desc := fs.String("m", "", "description")
	project := fs.String("project", "", "project slug — wins over the repo/directory you're standing in")
	parent := fs.String("parent", "", "group to add the new issue to")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr new <title> [-k kind] [-p priority] [-m description] [--project slug] [--parent GROUP]")
	}
	title := strings.Join(positional, " ")
	ref, err := c.CreateIssue(ctx, CreateIssueInput{
		Title: title, Description: *desc, Kind: *kind, Priority: *priority,
		ProjectSlug: *project, Locator: LocatorFrom(getenv, cwd()),
	})
	if err != nil {
		return err
	}

	if *parent != "" {
		if err := c.AddChild(ctx, *parent, ref.Ref); err != nil {
			// The issue exists either way. Say both facts rather than
			// failing the command, so the created ref is never lost.
			if *jsonOut {
				// stdout stays the bare ref JSON a script expects, but the
				// add failure still has to reach someone — say it on
				// stderr so it is never silently swallowed.
				fmt.Fprintf(stderr, "taskr: created %s but could not add it to %s: %v\n", ref.Ref, *parent, err)
				return printJSON(stdout, ref)
			}
			fmt.Fprintf(stdout, "Created %s — %s\n", ref.Ref, title)
			fmt.Fprintf(stdout, "Could not add it to %s: %v\n", *parent, err)
			return nil
		}
		if *jsonOut {
			return printJSON(stdout, ref)
		}
		fmt.Fprintf(stdout, "Created %s — %s (in group %s)\n", ref.Ref, title, *parent)
		return nil
	}

	if *jsonOut {
		return printJSON(stdout, ref)
	}
	fmt.Fprintf(stdout, "Created %s — %s\n", ref.Ref, title)
	return nil
}

func cmdStart(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine, session string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr start <ref>")
	}
	packet, err := c.StartWork(ctx, StartWorkInput{
		Issue: positional[0], Machine: machine, CWD: cwd(),
		Agent: agentCLI, AgentSessionID: session,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, packet)
	}
	fmt.Fprint(stdout, RenderResumePacket(packet))
	return nil
}

func cmdPark(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine, agentSession string) error {
	fs := flag.NewFlagSet("park", flag.ContinueOnError)
	fs.SetOutput(stderr)
	note := fs.String("m", "", "resume note — the next concrete action")
	reason := fs.String("r", "", "park reason")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *note == "" {
		return fmt.Errorf("usage: taskr park -m <note> [-r reason]")
	}

	session, issue, err := currentSession(ctx, c, machine, agentSession, false)
	if err != nil {
		return err
	}
	if err := c.ParkWork(ctx, ParkWorkInput{SessionID: session.ID, Reason: *reason, ResumeNote: *note}); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, okResult{OK: true})
	}
	if issue.Ref != "" {
		fmt.Fprintf(stdout, "Parked %s (%s).\n", issue.Ref, orDefault(*reason, "interrupted"))
	} else {
		fmt.Fprintf(stdout, "Parked session %s.\n", session.ID)
	}
	return nil
}

func cmdEnd(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine, agentSession string) error {
	fs := flag.NewFlagSet("end", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reason := fs.String("r", "", "why the session ended")
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	session, _, err := currentSession(ctx, c, machine, agentSession, true)
	if err != nil {
		return err
	}
	if err := c.EndWork(ctx, EndWorkInput{SessionID: session.ID, Reason: *reason}); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, okResult{OK: true})
	}
	fmt.Fprintf(stdout, "Ended session %s.\n", session.ID)
	return nil
}

// cmdClose finishes an issue. It exists because nothing else did: `end`
// closes the work SESSION and leaves the issue in_progress, and `triage`
// records whether a report was real — a different axis from whether the work
// is done. The only way through was a hand-written authenticated PATCH,
// which nothing documented, so finished issues sat in_progress and poisoned
// `taskr next` for the next reader (TSK-34).
//
// It deliberately does not end a live session on the issue. Ending a session
// as a side effect of another command is precisely what 678c8f0 removed, and
// the session may belong to another agent entirely. Saying so is the honest
// half of that choice.
func cmdClose(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine, agentSession string) error {
	fs := flag.NewFlagSet("close", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolution := fs.String("r", "", "how it ended, recorded on the issue")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr close <ref> [-r resolution]")
	}
	ref := positional[0]

	out, err := c.UpdateIssue(ctx, ref, UpdateIssueInput{Status: "closed", Resolution: *resolution})
	if err != nil {
		return err
	}
	if out.Ref != "" {
		ref = out.Ref
	}
	if *jsonOut {
		return printJSON(stdout, out)
	}

	fmt.Fprintf(stdout, "Closed %s.\n", ref)
	if out.GroupHint != nil {
		h := out.GroupHint
		if h.AllChildrenClosed {
			fmt.Fprintf(stdout, "All children of %s are closed — close it with `taskr close %s`.\n", h.ParentRef, h.ParentRef)
		} else if h.NextChild != nil {
			fmt.Fprintf(stdout, "Next in group %s: %s — %s (`taskr start %s`)\n",
				h.ParentRef, h.NextChild.Ref, h.NextChild.Title, h.NextChild.Ref)
		}
	}
	if session := liveSessionOn(ctx, c, machine, agentSession, ref); session != "" {
		fmt.Fprintf(stdout, "Session %s is still active on it — run `taskr end` to close it out.\n", session)
	}
	return nil
}

// liveSessionOn returns the caller's own active session when it is focused
// on ref, and "" otherwise.
//
// Errors are swallowed on purpose. This runs AFTER the close has already
// succeeded, and its only job is a courtesy line; turning a completed close
// into a failed command because a follow-up read failed would report the
// opposite of what happened. It also only ever sees the caller's own
// session — context is scoped by agent session id since 678c8f0 — so it
// claims nothing about another agent's.
func liveSessionOn(ctx context.Context, c *Client, machine, agentSession, ref string) string {
	v, err := c.Context(ctx, ContextQuery{Machine: machine, AgentSessionID: agentSession})
	if err != nil || v.ActiveSession == nil || v.ActiveIssue == nil {
		return ""
	}
	if !strings.EqualFold(v.ActiveIssue.Ref, ref) && v.ActiveIssue.ID != ref {
		return ""
	}
	return v.ActiveSession.ID
}

func cmdOffload(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine, agentSession string, getenv func(string) string) error {
	fs := flag.NewFlagSet("offload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	brief := fs.String("m", "", "brief a cold agent can act on")
	kind := fs.String("k", "", "issue kind")
	severity := fs.String("s", "", "severity")
	project := fs.String("project", "", "project slug — wins over the session's project and the locator")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || *brief == "" {
		return fmt.Errorf("usage: taskr offload <title> -m <brief> [-k kind] [-s severity] [--project slug]")
	}
	title := strings.Join(positional, " ")

	// A missing session costs the finding its provenance edge, not its
	// existence. An agent orienting on a cold machine has no session yet,
	// and orientation is when unrelated findings surface — refusing here
	// would drop them at the only moment they are ever noticed.
	session, _, err := currentSession(ctx, c, machine, agentSession, true)
	if err != nil && !errors.Is(err, ErrNoActiveSession) {
		return err
	}
	res, err := c.Offload(ctx, OffloadInput{
		SessionID: session.ID, Title: title, Brief: *brief, Kind: *kind, Severity: *severity,
		ProjectSlug: *project, Locator: LocatorFrom(getenv, cwd()),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, res)
	}
	fmt.Fprintf(stdout, "Offloaded %s — %s\n", res.Issue.Ref, title)
	if session.ID == "" {
		fmt.Fprintf(stdout, "No active session on %s, so it is filed without one.\n", machine)
	}
	return nil
}

func cmdComment(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("comment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	text := fs.String("m", "", "comment text")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || *text == "" {
		return fmt.Errorf("usage: taskr comment <ref> -m <text>")
	}
	if err := c.AddComment(ctx, positional[0], *text); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, okResult{OK: true})
	}
	fmt.Fprintf(stdout, "Commented on %s.\n", positional[0])
	return nil
}

func cmdTriage(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("triage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	evidence := fs.String("e", "", "evidence")
	dup := fs.String("d", "", "duplicate-of ref (required when verdict is duplicate)")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return fmt.Errorf("usage: taskr triage <ref> <verdict> [-e evidence] [-d duplicate-of]")
	}
	ref, verdict := positional[0], positional[1]
	if err := c.SubmitTriage(ctx, ref, SubmitTriageInput{Verdict: verdict, Evidence: *evidence, DuplicateOf: *dup}); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, okResult{OK: true})
	}
	fmt.Fprintf(stdout, "Recorded verdict %q for %s.\n", verdict, ref)
	return nil
}

// parseMeasure parses one --measure argument: metric=value with an
// optional trailing unit glued to the number ("list.p50=0.057s",
// "list.rps=462r/s"). The unit is whatever follows the longest prefix
// that parses as a float.
func parseMeasure(s, conditions string) (Measurement, error) {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return Measurement{}, fmt.Errorf("--measure wants metric=value[unit], got %q", s)
	}
	metric, rest := s[:eq], s[eq+1:]
	if rest == "" {
		return Measurement{}, fmt.Errorf("--measure %s has no value", metric)
	}
	// Longest numeric prefix: try the whole string, then shrink.
	for end := len(rest); end > 0; end-- {
		v, err := strconv.ParseFloat(rest[:end], 64)
		if err == nil {
			return Measurement{Metric: metric, Value: v, Unit: rest[end:], Conditions: conditions}, nil
		}
	}
	return Measurement{}, fmt.Errorf("--measure %s: %q is not a number", metric, rest)
}

// runCheck dispatches `taskr check add|ls|run`.
func runCheck(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taskr check add|ls|run …")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return cmdCheckAdd(ctx, c, rest, stdout, stderr)
	case "ls":
		return cmdCheckLs(ctx, c, rest, stdout, stderr)
	case "run":
		return cmdCheckRun(ctx, c, rest, stdout, stderr)
	default:
		return fmt.Errorf("usage: taskr check add|ls|run …")
	}
}

func cmdCheckAdd(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("check add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	title := fs.String("t", "", "short name for the check (defaults to the procedure)")
	procedure := fs.String("m", "", "how to run it — the command or steps, verbatim")
	expect := fs.String("expect", "", "what passing looks like")
	human := fs.Bool("human", false, "only a person can run this check")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || *procedure == "" && *title == "" {
		return fmt.Errorf("usage: taskr check add <ref> -m <procedure> [--expect <text>] [--human] [-t <title>]")
	}
	name := *title
	if name == "" {
		name = *procedure
	}
	runner := "agent"
	if *human {
		runner = "human"
	}
	ref, err := c.DefineCheck(ctx, positional[0], DefineCheckInput{
		Title: name, Procedure: *procedure, Expect: *expect, Runner: runner,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, ref)
	}
	fmt.Fprintf(stdout, "Check %s on %s (%s). Record a result with `taskr check run %s --pass|--fail`.\n",
		ref.ID, ref.Issue.Ref, runner, ref.ID)
	return nil
}

func cmdCheckLs(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("check ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr check ls <ref>")
	}
	checks, err := c.ListChecks(ctx, positional[0])
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, checks)
	}
	RenderChecks(stdout, checks)
	return nil
}

func cmdCheckRun(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("check run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pass := fs.Bool("pass", false, "the check passed")
	failFlag := fs.Bool("fail", false, "the check failed")
	var measures stringList
	fs.Var(&measures, "measure", "metric=value[unit], repeatable")
	conditions := fs.String("conditions", "", "conditions the measurements were taken under")
	evidence := fs.String("e", "", "evidence document id")
	sha := fs.String("sha", "", "head SHA the run verified")
	image := fs.String("image", "", "image digest the run verified")
	note := fs.String("note", "", "free-text note")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || *pass == *failFlag {
		return fmt.Errorf("usage: taskr check run <check-id> --pass|--fail [--measure metric=value[unit]]… [--conditions <text>] [-e <doc-id>] [--sha <head>] [--image <digest>] [--note <text>]")
	}
	outcome := "pass"
	if *failFlag {
		outcome = "fail"
	}
	var ms []Measurement
	for _, raw := range measures {
		m, err := parseMeasure(raw, *conditions)
		if err != nil {
			return err
		}
		ms = append(ms, m)
	}
	run, err := c.RunCheck(ctx, positional[0], RunCheckInput{
		Outcome: outcome, Measurements: ms, EvidenceDocID: *evidence,
		HeadSHA: *sha, Image: *image, Note: *note,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, run)
	}
	fmt.Fprintf(stdout, "Recorded %s (run %s).\n", outcome, run.ID)
	return nil
}

func cmdTimeline(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("timeline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr timeline <ref>")
	}
	rows, err := c.Timeline(ctx, positional[0])
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, rows)
	}
	RenderTimeline(stdout, rows)
	return nil
}

// runGroup dispatches `taskr group add|rm`. A group is an ordinary issue of
// kind group; these verbs only manage membership.
func runGroup(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: taskr group add|rm <group> <child>")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("group "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, rest)
	if err != nil {
		return err
	}
	if len(positional) < 2 {
		return fmt.Errorf("usage: taskr group %s <group> <child>", sub)
	}
	parent, child := positional[0], positional[1]

	switch sub {
	case "add":
		if err := c.AddChild(ctx, parent, child); err != nil {
			return err
		}
		if *jsonOut {
			return printJSON(stdout, okResult{OK: true})
		}
		fmt.Fprintf(stdout, "Added %s to %s.\n", child, parent)
	case "rm":
		if err := c.RemoveChild(ctx, parent, child); err != nil {
			return err
		}
		if *jsonOut {
			return printJSON(stdout, okResult{OK: true})
		}
		fmt.Fprintf(stdout, "Removed %s from %s.\n", child, parent)
	default:
		return fmt.Errorf("usage: taskr group add|rm <group> <child>")
	}
	return nil
}

// runDoc dispatches `taskr doc`. The bare form — `taskr doc <ref>` — is the
// read that existed first and keeps working; add and show are subcommands
// because a document id is never spelled "add" or "show".
func runDoc(ctx context.Context, c *Client, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "add":
			return cmdDocAdd(ctx, c, args[1:], stdin, stdout, stderr)
		case "show":
			return cmdDocShow(ctx, c, args[1:], stdout, stderr)
		}
	}
	return cmdDoc(ctx, c, args, stdout, stderr)
}

func cmdDoc(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr doc <ref>")
	}
	rows, err := c.ListDocuments(ctx, positional[0])
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, rows)
	}
	RenderDocuments(stdout, rows)
	return nil
}

// cmdDocAdd attaches a document to an issue. Until this existed the only way
// to do it was a hand-written authenticated POST, and a workflow step that
// requires hand-written JSON is a step that keeps being skipped — it was
// skipped for TSK-24, whose spec sat committed in git with nothing in taskr
// pointing at it (TSK-25).
//
// The body is sent, never the path: a reader on another machine has the
// issue and not your checkout.
func cmdDocAdd(ctx context.Context, c *Client, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doc add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("f", "", "file holding the body, or - for stdin")
	// spec is the default because this verb exists to stop specs and plans
	// going unattached, and requiring the flag reintroduces the friction
	// that got the step skipped. A wrong type is visible in `taskr doc
	// <ref>` and revisable; a document nobody attached is not.
	docType := fs.String("t", "spec", "spec, plan or note")
	title := fs.String("title", "", "title; defaults to the body's first heading, then the file name")
	docID := fs.String("id", "", "revise this document instead of creating one")
	diff := fs.String("diff", "", "short note on what changed, when revising")
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || *path == "" {
		return fmt.Errorf("usage: taskr doc add <ref> -f <path> [-t spec|plan|note] [--title T]")
	}
	ref := positional[0]

	body, err := readBody(*path, stdin)
	if err != nil {
		return err
	}
	name := *title
	if name == "" {
		name = deriveTitle(body, *path)
	}
	if name == "" {
		return fmt.Errorf("no title: the body has no heading and -f is stdin, so pass --title")
	}

	out, err := c.UpsertDocument(ctx, UpsertDocumentInput{
		DocumentID: *docID, Type: *docType, Title: name, Body: body,
		DiffSummary: *diff, LinkIssue: ref,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, out)
	}
	fmt.Fprintf(stdout, "Attached %s %q to %s as %s.\n", *docType, name, ref, out.ID)
	return nil
}

func cmdDocShow(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doc show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("usage: taskr doc show <id>")
	}
	v, err := c.GetDocument(ctx, positional[0])
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, v)
	}
	RenderDocument(stdout, v)
	return nil
}

// readBody reads the document body from a file, or from stdin when path is
// "-". A read that fails returns before any request goes out: nothing should
// be written when the body could not be read, and the error names the path
// the caller typed rather than whatever the server would have said about an
// empty body.
func readBody(path string, stdin io.Reader) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("reading the body from stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// deriveTitle takes the body's first markdown heading, falling back to the
// file name without its extension. It exists so attaching a spec does not
// mean retyping its title — the friction that gets the step skipped is the
// whole bug. Stdin with no heading derives nothing, and the caller is asked
// for --title rather than given a document called "-".
func deriveTitle(body, path string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if heading, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(heading)
		}
	}
	if path == "-" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// runAuth dispatches `taskr auth <subcommand>`. login is the only one today.
func runAuth(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: taskr auth login | taskr auth status")
	}
	switch args[0] {
	case "login":
		return authLogin(os.Stdin, args[1:], stdout, stderr, getenv)
	case "status":
		return authStatusCmd(args[1:], stdout, stderr, getenv)
	default:
		return fmt.Errorf("usage: taskr auth login | taskr auth status")
	}
}

// authStatusCmd answers "who do my writes come out as", without performing
// one.
//
// That question had no answer before TSK-38: the actor lives on the key, a
// key minted without the optional actor argument defaults to the user, and
// nothing said so — leaving "write a comment, read it back, notice the
// colour is wrong" as the discovery path, by which point the ledger already
// carries the wrong name. So the remedy is printed alongside the finding
// rather than left to be looked up, and with the key id already filled in.
func authStatusCmd(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	target, err := resolveTarget(getenv, stderr)
	if err != nil {
		return err
	}
	client := &Client{BaseURL: target.BaseURL, Key: target.Key}
	st, err := client.AuthStatus(context.Background())
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(stdout, st)
	}

	fmt.Fprintf(stdout, "host      %s\n", target.BaseURL)
	if !st.Authenticated {
		fmt.Fprintln(stdout, "          not authenticated")
		if st.Required {
			fmt.Fprintln(stdout, "          this instance requires a key — `taskr auth login` stores one")
		} else {
			fmt.Fprintln(stdout, "          this instance has no keys, so it is open to anyone who can reach it")
		}
		return nil
	}
	if st.Actor == "" {
		// Authenticated, but the instance said nothing about the actor —
		// it predates TSK-38. Printing "writes as" with a blank after it
		// reads as an answer, which is the failure this command exists to
		// end, so name the reason and send the reader at the server rather
		// than at the key.
		fmt.Fprintln(stdout, "          authenticated, but this instance does not report the actor")
		fmt.Fprintln(stdout, "          (it predates `taskr auth status`) — upgrade it to see who you write as")
		return nil
	}
	fmt.Fprintf(stdout, "writes as %s\n", st.Actor)
	if len(st.Scopes) > 0 {
		fmt.Fprintf(stdout, "scopes    %s\n", strings.Join(st.Scopes, ", "))
	}
	if st.KeyID != "" {
		fmt.Fprintf(stdout, "key       %s\n", st.KeyID)
	}
	// "exempt" is what a billing-off install and a comped org both report,
	// and neither has a plan to speak of. Printing "plan exempt" there would
	// make a self-hosted taskr look like it has a subscription it does not
	// have, and would change what this command prints on an install where
	// nothing about billing is switched on — so the line is skipped entirely
	// and the output is exactly what it was before billing existed.
	if b := st.Billing; b != nil && b.Status != "exempt" {
		line := b.Status
		switch {
		// The date is sliced to its yyyy-mm-dd prefix, so the length is
		// checked rather than assumed: TrialEndsAt comes off the wire, and a
		// short or malformed value would panic this command rather than
		// print a slightly wrong date.
		case b.Status == "trial" && len(b.TrialEndsAt) >= 10:
			line = "trial, ends " + b.TrialEndsAt[:10]
		// Both locked cases say nothing has been deleted, because this
		// command is where someone comes to find out why everything stopped
		// working — and "trial expired" alone reads as "your work is gone"
		// to someone who has just been refused all of it.
		case b.Status == "trial_expired":
			line = "trial expired — this org is locked; subscribe from the web app. Nothing has been deleted"
		case b.Status == "lapsed":
			line = "subscription lapsed — this org is locked; manage billing from the web app. Nothing has been deleted"
		}
		fmt.Fprintf(stdout, "plan      %s\n", line)
	}
	if st.Actor == "user" && st.KeyID != "" {
		fmt.Fprintf(stdout,
			"\nIf an agent is holding this key, every write it makes is recorded as the user.\n"+
				"Relabel it with: taskr-admin key actor %s agent\n", st.KeyID)
	}
	return nil
}

// authLogin reads a key from stdin — never argv, which would leave it in
// shell history and visible in `ps` — verifies it against the target host,
// and stores it in hosts.json keyed by that host.
func authLogin(stdin io.Reader, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("reading key from stdin: %w", err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return fmt.Errorf("no key on stdin — pipe it in, e.g. `echo $TASKR_KEY | taskr auth login`")
	}

	target, err := resolveTarget(getenv, stderr)
	if err != nil {
		return err
	}
	client := &Client{BaseURL: target.BaseURL, Key: key}
	if err := client.Login(context.Background(), key); err != nil {
		return err
	}
	if err := saveKey(target.Host, key); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Logged in to %s.\n", target.Host)
	return nil
}
