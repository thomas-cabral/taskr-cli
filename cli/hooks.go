// cli/hooks.go
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// The two verbs a harness runs on the model's behalf, and the rules they
// both live by:
//
//   - They never print. A hook's output is injected into the session, and
//     a line about taskr in the middle of someone's work is noise at best
//     and a distraction the model acts on at worst.
//   - They never fail. Every one of them exits 0 whatever happened —
//     unreachable API, no credential, no session, a 500. taskr being down
//     must be invisible from inside a session, because the alternative is a
//     hook erroring on every turn for as long as the outage lasts.
//   - They are bounded. hookTimeout caps the whole call, so a hung host
//     costs a second and not the turn.
//
// What they must NOT do is decide anything. The note park --auto composes is
// assembled from facts the CLI can read; where a judgment would be needed it
// says nothing instead of guessing, and the model's own park -m is what
// carries meaning.
const hookTimeout = time.Second

// hookPayload is the part of a harness's hook JSON taskr reads. Claude Code
// writes the whole event to the hook's stdin; every other field is ignored.
//
// This is read because the hook shell is not guaranteed to carry
// CLAUDE_CODE_SESSION_ID — agentSessionID reads the environment, and in hook
// context the environment may not have it. Falling back to the parent pid
// there would open a second work session per turn, each one a stranger to
// the last. The payload is the authority when it has an answer.
type hookPayload struct {
	SessionID string `json:"session_id"`
}

// hookStdin is stdin when something is piped into it, and nil when it is a
// terminal. A hook is always piped; a person running `taskr touch` by hand at
// a prompt is not, and must not be left blocking on a read that will never
// return.
func hookStdin() io.Reader {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return os.Stdin
}

// hookSessionID pulls the harness's session id out of a hook payload, and
// answers "" for anything it does not recognise — no stdin, empty stdin,
// something that is not JSON, JSON without the field. Never an error: this
// is one of two ways to learn the same thing, and the environment is the
// other.
func hookSessionID(r io.Reader) string {
	if r == nil {
		return ""
	}
	// Bounded because stdin is whatever the harness wrote. A hook event is
	// a few hundred bytes; anything past this is not one.
	raw, err := io.ReadAll(io.LimitReader(r, 64<<10))
	if err != nil || len(raw) == 0 {
		return ""
	}
	var p hookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	return strings.TrimSpace(p.SessionID)
}

// cmdTouch says the caller's session is still alive.
//
// It is addressed by machine and agent session id rather than by session id,
// so it is exactly one request: the server resolves which session that pair
// opened. Learning the id first would mean a GET /api/context per turn, which
// is a real query spent to send a timestamp.
//
// Every failure path returns nil. That is not sloppiness — it is the contract
// in the comment at the top of this file, and the only way a hook can be safe
// to plant on every turn of every session on a machine.
func cmdTouch(ctx context.Context, c *Client, args []string, stdout, stderr io.Writer, machine, agentSession string, stdin io.Reader) error {
	fs := flag.NewFlagSet("touch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Accepted so a person debugging a hook can see what it does. The hooks
	// themselves never pass it, and without it touch prints nothing at all.
	verbose := fs.Bool("v", false, "report what happened, for debugging a hook by hand")
	if _, err := parseFlags(fs, args); err != nil {
		return nil
	}
	if id := hookSessionID(stdin); id != "" {
		agentSession = id
	}

	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	err := c.Touch(ctx, TouchWorkInput{Machine: machine, AgentSessionID: agentSession})
	if *verbose {
		if err != nil {
			fmt.Fprintf(stderr, "taskr: touch: %v\n", err)
		} else {
			fmt.Fprintf(stdout, "touched %s · %s\n", machine, agentSession)
		}
	}
	return nil
}

// autoParkNote is the resume note the harness writes when a session ends
// without anybody stopping deliberately.
//
// Every clause is a fact the CLI can read: the branch and head come from the
// checkout (envWithRepo falls back to .git, so a hook shell with nothing
// exported still gets them), the dirty list only from TASKR_DIRTY because
// answering it needs git itself, and the step from the plan the server
// already returned. Nothing here is inferred, because the one thing an
// automatic note must never do is read like a human wrote it.
//
// The prefix is deliberate and the reason for the auto flag next to it: a
// reader who sees this text knows nobody chose to stop, and the flag lets
// `start` say so without parsing prose.
func autoParkNote(getenv func(string) string, progress *StepProgress) string {
	clauses := []string{}
	if branch, head := getenv("TASKR_BRANCH"), getenv("TASKR_HEAD"); head != "" {
		if branch == "" {
			branch = "(detached)"
		}
		clauses = append(clauses, fmt.Sprintf("%s @ %s", branch, shortSHA(head)))
	}
	if dirty := dirtyFiles(getenv("TASKR_DIRTY")); len(dirty) > 0 {
		clauses = append(clauses, "dirty: "+strings.Join(dirty, ", "))
	}
	if progress != nil && progress.Current != nil {
		clauses = append(clauses, fmt.Sprintf("step %d/%d %q in progress",
			progress.Current.Position, progress.Total, progress.Current.Title))
	}
	note := "auto-park (session ended)"
	if len(clauses) > 0 {
		note += ": " + strings.Join(clauses, "; ")
	}
	// Said in the note as well as on the event, because the note is what a
	// human reads in the parked panel and the flag is what start renders.
	return note + ". No human note — read the diff before trusting where this stopped."
}

// autoPark parks the caller's own active session on the way out, if there is
// one. It is park's --auto path, kept here with the other hook code because
// it obeys the same three rules: silent, bounded, and exit 0 whatever
// happened — including when there is no session, which is the ordinary state
// of a harness closing a window nobody worked in.
func autoPark(ctx context.Context, c *Client, stdout, stderr io.Writer, machine, agentSession string, getenv func(string) string, stdin io.Reader, verbose bool) error {
	if id := hookSessionID(stdin); id != "" {
		agentSession = id
	}
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	report := func(err error) error {
		if verbose && err != nil {
			fmt.Fprintf(stderr, "taskr: park --auto: %v\n", err)
		}
		return nil
	}

	v, err := c.Context(ctx, ContextQuery{Machine: machine, AgentSessionID: agentSession})
	if err != nil {
		return report(err)
	}
	// No active session is not a failure. A window closing on a machine
	// where nobody ran `taskr start` is most of the SessionEnd events this
	// will ever see.
	if v.ActiveSession == nil {
		if verbose {
			fmt.Fprintln(stdout, "no active session — nothing to park")
		}
		return nil
	}

	var progress *StepProgress
	if v.ActiveIssue != nil {
		progress = v.ActiveIssue.StepProgress
	}
	if err := c.ParkWork(ctx, ParkWorkInput{
		SessionID: v.ActiveSession.ID,
		// interrupted, not done_for_now: nobody said the work was done, and
		// a reason is the one field here that would be a guess if it claimed
		// more than "this stopped".
		Reason:     "interrupted",
		ResumeNote: autoParkNote(getenv, progress),
		Snapshot:   gitSnapshot(getenv),
		Auto:       true,
	}); err != nil {
		return report(err)
	}
	if verbose {
		fmt.Fprintf(stdout, "auto-parked session %s\n", v.ActiveSession.ID)
	}
	return nil
}

// shortSHA renders a commit the way the rest of taskr does: enough to
// identify it, not so much that it swallows the line it is in.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
