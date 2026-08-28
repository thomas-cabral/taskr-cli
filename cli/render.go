// cli/render.go
package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// RenderResumePacket is the prose the resume packet becomes for a human —
// this output is the product of `taskr start`, so it leads with why work
// stopped and what to do next, then the tree state, then the context
// entries. Everything after that (graph, prior sessions, documents) is
// supporting detail for a reader who wants more.
func RenderResumePacket(p ResumePacket) string {
	var b strings.Builder
	iv := p.Issue

	fmt.Fprintf(&b, "%s — %s\n", iv.Ref, iv.Title)
	fmt.Fprintf(&b, "status: %s   priority: %s   kind: %s\n\n", iv.Status, iv.Priority, iv.Kind)

	if p.LastPark != nil {
		fmt.Fprintf(&b, "Why work stopped: %s (parked %s)\n", p.LastPark.Reason, p.LastPark.ParkedAt)
		if p.LastPark.ResumeNote != "" {
			fmt.Fprintf(&b, "What to do next:\n  %s\n", indent(p.LastPark.ResumeNote))
		} else {
			b.WriteString("No resume note was left — check the timeline for what happened last.\n")
		}
		if p.LastPark.ResumeCommand != "" {
			fmt.Fprintf(&b, "Re-enter that agent session: %s\n", p.LastPark.ResumeCommand)
		}
	} else {
		b.WriteString("This is a fresh start — no prior park on this issue.\n")
	}
	b.WriteString("\n")

	// Directly under what to do next, and above everything else, because
	// the two answer the same question: the note says where to go, and the
	// dead ends say which roads are already known to end. Anything printed
	// between them is read after the agent has started deciding.
	renderCatchupSection(&b, p.Catchup)

	if iv.Snapshot != nil {
		s := iv.Snapshot
		fmt.Fprintf(&b, "Tree state (as of %s):\n", s.RecordedAt)
		fmt.Fprintf(&b, "  %s @ %s  %s\n", s.Repo, branchOrDetached(s.Branch), s.HeadSHA)
		if s.Worktree != "" {
			fmt.Fprintf(&b, "  worktree: %s\n", s.Worktree)
		}
		if s.MergeBase != "" {
			fmt.Fprintf(&b, "  merge-base: %s\n", s.MergeBase)
		}
		if n := len(s.DirtyFiles); n > 0 {
			fmt.Fprintf(&b, "  %d dirty file(s): %s\n", n, strings.Join(s.DirtyFiles, ", "))
		}
	} else {
		b.WriteString("Tree state: no git snapshot has been recorded for this issue.\n")
	}
	b.WriteString("\n")

	if len(iv.AgentContext) > 0 {
		fmt.Fprintf(&b, "Agent context (%d entr%s):\n", len(iv.AgentContext), plural(len(iv.AgentContext), "y", "ies"))
		for _, e := range iv.AgentContext {
			fmt.Fprintf(&b, "  [%s] %s\n", e.Kind, indent(e.Body))
		}
		b.WriteString("\n")
	}

	if iv.Description != "" {
		fmt.Fprintf(&b, "Description:\n  %s\n\n", indent(iv.Description))
	}

	if iv.Group != nil {
		renderGroup(&b, iv.Ref, iv.Group)
		b.WriteString("\n")
	}

	if rel := renderGraphSummary(p.Graph); rel != "" {
		fmt.Fprintf(&b, "Related issues:\n%s\n", rel)
	}

	if len(p.PriorSessions) > 0 {
		b.WriteString("Prior sessions on this issue:\n")
		for _, s := range p.PriorSessions {
			fmt.Fprintf(&b, "  %s on %s (%s)\n", s.ID, s.Machine, s.Status)
		}
		b.WriteString("\n")
	}

	if len(p.Documents) > 0 {
		b.WriteString("Documents:\n")
		for _, d := range p.Documents {
			fmt.Fprintf(&b, "  [%s] %s (%s)\n", d.Type, d.Title, d.ID)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Session %s on %s", p.Session.ID, p.Session.Machine)
	if p.Session.CWD != "" {
		fmt.Fprintf(&b, " (%s)", p.Session.CWD)
	}
	b.WriteString("\n")

	return b.String()
}

func renderGraphSummary(g GraphContext) string {
	var b strings.Builder
	writeRel := func(label string, rows []RelatedIssue) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(&b, "  %s:\n", label)
		for _, r := range rows {
			fmt.Fprintf(&b, "    %s %s (%s)\n", r.Ref, r.Title, r.Status)
		}
	}
	writeRel("blocks", g.Blocks)
	writeRel("blocked by", g.BlockedBy)
	writeRel("relates to", g.RelatesTo)
	writeRel("discovered during", g.DiscoveredDuring)
	writeRel("discovered", g.Discovered)
	writeRel("children", g.Children)
	writeRel("parent", g.Parent)
	return b.String()
}

// renderGroup writes a group's ordered child walk — shared by
// RenderResumePacket (`taskr start` on the group) and RenderIssue (`taskr
// show` on the group), so the two commands describe the same walk the same
// way. ref is the group's own ref, used only for the all-closed hint.
func renderGroup(b *strings.Builder, ref string, g *GroupBlock) {
	fmt.Fprintf(b, "Group walk (%d/%d closed):\n", g.Closed, g.Total)
	for _, c := range g.Children {
		marker := "○"
		if c.Status == "closed" {
			marker = "✓"
		}
		fmt.Fprintf(b, "  %d. %s %s — %s (%s)\n", c.Position, marker, c.Ref, c.Title, c.Status)
	}
	if g.NextChild != nil {
		fmt.Fprintf(b, "Next: taskr start %s\n", g.NextChild.Ref)
	} else if g.Total > 0 {
		fmt.Fprintf(b, "All children closed — close the group with `taskr close %s`.\n", ref)
	}
}

// RenderContext renders `taskr context` for a human. actor is who the
// caller's credential writes as, on the first line because orientation is
// the last cheap moment to notice it is the wrong one; empty when the
// instance did not say, which reads as one fewer field rather than a claim.
func RenderContext(v ContextView, actor string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "machine: %s   open issues: %d", v.Machine, v.OpenIssues)
	if actor != "" {
		fmt.Fprintf(&b, "   writes as: %s", actor)
	}
	b.WriteString("\n")

	if v.ActiveSession != nil {
		s := v.ActiveSession
		fmt.Fprintf(&b, "\nActive session %s", s.ID)
		if s.IssueID != "" && v.ActiveIssue != nil {
			fmt.Fprintf(&b, " on %s — %s (%s)", v.ActiveIssue.Ref, v.ActiveIssue.Title, v.ActiveIssue.Status)
			if p := v.ActiveIssue.Parent; p != nil {
				fmt.Fprintf(&b, " — in group %s (%d/%d closed)", p.Ref, p.Closed, p.Total)
			}
		}
		b.WriteString("\n")
		// Orientation is where an agent decides what it is doing next, so
		// it is where a plan nobody has kept gets said — under the session
		// it belongs to, before anything else competes for attention.
		renderUntouchedPlan(&b, v.UntouchedPlan)
	} else {
		b.WriteString("\nNo active session on this machine. Run `taskr next` or `taskr ls`, then `taskr start <ref>`.\n")
	}

	if len(v.Parked) > 0 {
		fmt.Fprintf(&b, "\nParked sessions (%d, newest first):\n", len(v.Parked))
		for _, s := range v.Parked {
			// One line per row: what it was, why it stopped, how long ago.
			// Orientation is printed at every session start, and rendering
			// resume notes here measured ~1,400 tokens of mostly stale
			// detail (TSK-220) — which row deserves detail is the caller's
			// call, made through taskr show or taskr start. The note still
			// travels in --json and in the resume packet, where a reader
			// has already committed to the row.
			head := s.IssueRef + " — " + s.IssueTitle
			if s.IssueRef == "" {
				if s.IssueID != "" {
					// An older server sends the raw issue id and nothing
					// else; the uuid is less than a ref but better than a
					// hole.
					head = "issue " + s.IssueID + " on " + s.Machine
				} else {
					head = "session " + s.ID + " on " + s.Machine
				}
			}
			var parts []string
			if age := relativeAge(time.Now(), s.ParkedAt); age != "" {
				parts = append(parts, "parked "+age)
			}
			if s.Reason != "" {
				parts = append(parts, parkReason(s.Reason))
			}
			if s.AlsoParked > 0 {
				parts = append(parts, fmt.Sprintf("also parked: %d", s.AlsoParked))
			}
			if len(parts) > 0 {
				fmt.Fprintf(&b, "  %s (%s)\n", head, strings.Join(parts, " · "))
			} else {
				fmt.Fprintf(&b, "  %s\n", head)
			}
		}
	}

	if v.Project != nil {
		fmt.Fprintf(&b, "\nProject: %s (%s)\n", v.Project.Name, v.Project.Slug)
		// Orientation is where an agent decides what to branch from and
		// where to send the PR, and this is the only place that knows.
		renderConventions(&b, "  ", v.Project.Conventions)
	} else if v.SetupHint != nil {
		fmt.Fprintf(&b, "\n%s\n", v.SetupHint.Reason)
		for _, c := range v.SetupHint.Collect {
			fmt.Fprintf(&b, "  %s\n", c)
		}
	} else if v.Ambiguous != nil {
		fmt.Fprintf(&b, "\n%s\n", v.Ambiguous.Reason)
	}

	return b.String()
}

// renderUntouchedPlan is the one wording every surface uses for a plan
// that has not moved (TSK-139): `taskr context` prints it under the active
// session, `taskr park` prints it before parking. It names the count, the
// issue and the verb that fixes it, because the reader is an agent mid-task
// and the useful answer is a command, not a lecture. Nil prints nothing.
func renderUntouchedPlan(w io.Writer, u *UntouchedPlan) {
	if u == nil {
		return
	}
	fmt.Fprintf(w, "Plan untouched: %d step(s) still open on %s and none moved since this session started (%s).\n", u.Open, u.IssueRef, u.Since)
	fmt.Fprintf(w, "  If any of it landed, say so now — `taskr step done %s <pos> -m \"<what landed>\"` — or the next reader inherits a plan that lies.\n", u.IssueRef)
}

// RenderIssueList renders `taskr ls` and `taskr next`-shaped rows as a table.
func RenderIssueTable(w io.Writer, rows []SearchResult) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "REF\tSTATUS\tKIND\tPRIORITY\tTITLE")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Ref, r.Status, r.Kind, r.Priority, r.Title)
	}
	tw.Flush()
}

// RenderCandidates renders `taskr next`.
func RenderCandidates(w io.Writer, rows []Candidate) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "REF\tSCORE\tPRIORITY\tTITLE\tREASONS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%.1f\t%s\t%s\t%s\n", r.Ref, r.Score, r.Priority, r.Title, strings.Join(r.Reasons, "; "))
	}
	tw.Flush()
}

// RenderTriageQueue renders `taskr triage` with no verdict: what needs a
// look, and why. The WHY column says the reason in words a reader acts on
// rather than the wire token — the same labels the app's triage screen
// uses — and carries the evidence for it: the two SHAs for rot, the date
// of the verdict that aged out for expired.
//
// A TWIN column appears only when some row has one: the closest open issue
// the server found nothing already connecting it to, scored the way every
// suggestion is (TSK-178). It is the duplicate net at scan time — a pair
// sitting in the queue as two rows reads as one before either is opened —
// and the footer names the verdict that collapses it.
func RenderTriageQueue(w io.Writer, rows []TriageCandidate) {
	twins := false
	for _, r := range rows {
		twins = twins || r.Twin != nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	if twins {
		fmt.Fprintln(tw, "REF\tREASON\tTITLE\tWHY\tTWIN")
	} else {
		fmt.Fprintln(tw, "REF\tREASON\tTITLE\tWHY")
	}
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s", r.IssueRef, r.Reason, r.Title, triageWhy(r))
		if twins {
			fmt.Fprintf(tw, "\t%s", twinCell(r.Twin))
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()
	if twins {
		fmt.Fprintln(w, "\nTWIN is the closest open issue nothing already links to. Same work? `taskr triage <ref> duplicate -d <twin>`.")
	}
}

// twinCell is the score-then-ref shape RenderSimilar uses, so a twin reads
// the same in the queue as it does under `taskr triage <ref>`.
func twinCell(n *Neighbor) string {
	if n == nil {
		return ""
	}
	return fmt.Sprintf("%.2f %s", n.Score, n.Ref)
}

func triageWhy(r TriageCandidate) string {
	switch r.Reason {
	case "new":
		return "never triaged"
	case "rot":
		if r.SnapshotSHA != "" && r.LatestSHA != "" {
			return fmt.Sprintf("the branch moved under it: %s -> %s", short(r.SnapshotSHA), short(r.LatestSHA))
		}
		return "the branch moved under it"
	case "expired":
		if r.TriagedAt != "" {
			return "its verdict aged out (triaged " + dateOf(r.TriagedAt) + ")"
		}
		return "its verdict aged out"
	}
	return r.Reason
}

// dateOf trims an RFC 3339 timestamp to its date: the day a verdict was
// recorded is what a reader compares against, not the second.
func dateOf(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// relativeAge renders a wire timestamp as the age a reader acts on:
// "just now", "45m ago", "3h ago", "2d ago", then the calendar date once
// the days stop fitting on one line. now is a parameter so tests can fix
// the clock; an empty or unparseable timestamp renders as no age at all —
// a bad one must not crash a render that is otherwise useful.
func relativeAge(now time.Time, ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("Jan 2")
}

// parkReason says why work stopped in the reader's words. The tokens are
// the API's reason codes; only the two that read as code get unwrapped,
// the rest already speak for themselves.
func parkReason(r string) string {
	switch r {
	case "done_for_now":
		return "done for now"
	case "context_exhausted":
		return "context exhausted"
	}
	return r
}

// RenderIssue renders `taskr show`.
func RenderIssue(v IssueView, withContext bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", v.Ref, v.Title)
	fmt.Fprintf(&b, "status: %s   priority: %s   kind: %s\n", v.Status, v.Priority, v.Kind)
	if v.Parent != nil {
		fmt.Fprintf(&b, "group: %s (%d/%d closed)\n", v.Parent.Ref, v.Parent.Closed, v.Parent.Total)
	}
	if v.Resolution != "" {
		fmt.Fprintf(&b, "resolution: %s\n", v.Resolution)
	}
	if v.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", v.Description)
	}
	if v.Group != nil {
		b.WriteString("\n")
		renderGroup(&b, v.Ref, v.Group)
	}
	if v.Snapshot != nil {
		s := v.Snapshot
		fmt.Fprintf(&b, "\ntree state: %s @ %s %s (as of %s)\n", s.Repo, branchOrDetached(s.Branch), s.HeadSHA, s.RecordedAt)
	}
	if len(v.Comments) > 0 {
		fmt.Fprintf(&b, "\nComments (%d):\n", len(v.Comments))
		for _, c := range v.Comments {
			fmt.Fprintf(&b, "  [%s] %s: %s\n", c.CreatedAt, c.Actor, c.Body)
		}
	}
	if withContext && len(v.AgentContext) > 0 {
		fmt.Fprintf(&b, "\nAgent context (%d):\n", len(v.AgentContext))
		for _, e := range v.AgentContext {
			fmt.Fprintf(&b, "  [%s] %s\n", e.Kind, e.Body)
		}
	}
	return b.String()
}

// RenderTimeline renders `taskr timeline`.
func RenderTimeline(w io.Writer, rows []TimelineEntry) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tTYPE\tACTOR\tSUMMARY")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.OccurredAt, r.Type, r.Actor, r.Summary)
	}
	tw.Flush()
}

// RenderDocuments renders `taskr doc`.
func RenderDocuments(w io.Writer, rows []DocumentRef) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tTITLE")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, r.Type, r.Title)
	}
	tw.Flush()
}

// RenderChecks renders `taskr check ls`: an issue's checks, with the
// outcome and date of the latest run so a reader can tell what still
// needs one without opening each check.
func RenderChecks(w io.Writer, checks []CheckView) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tRUNNER\tSTATUS\tLAST RUN")
	for _, c := range checks {
		last := "—"
		if c.LatestRun != nil {
			last = c.LatestRun.Outcome
			if c.LatestRun.RecordedAt != "" {
				last += " " + c.LatestRun.RecordedAt[:10]
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.ID, c.Title, c.Runner, c.Status, last)
	}
	tw.Flush()
}

// RenderDocument prints one document for reading. The header is one line so
// the body starts at the top of the terminal — a spec is read, not scanned,
// and anything above it is in the way. Revisions and the superseding
// document are named only when they exist, because "revisions: 0" tells a
// reader nothing they did not assume.
func RenderDocument(w io.Writer, v DocumentView) {
	fmt.Fprintf(w, "%s — %s (%s)\n", v.ID, v.Title, v.Type)
	if v.Revisions > 0 {
		fmt.Fprintf(w, "revised %d time(s), last %s\n", v.Revisions, v.UpdatedAt)
	}
	if v.SupersededBy != "" {
		fmt.Fprintf(w, "superseded by %s\n", v.SupersededBy)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.TrimRight(v.Body, "\n"))
}

// RenderDocumentRevisions renders `taskr doc history`: one row per
// revision, oldest first, so a reader can see how a spec reached the body
// `doc show` prints. Revision one is the original body; a summary column
// only shows when some revision carries one, mirroring how RenderDocument
// omits counts that carry no information.
func RenderDocumentRevisions(w io.Writer, docID string, rows []DocumentRevision) {
	fmt.Fprintf(w, "%s — %d revision(s)\n", docID, len(rows))
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	header := "REV\tSHA256\tAT"
	hasSummaries := false
	for _, r := range rows {
		if r.DiffSummary != "" {
			hasSummaries = true
			break
		}
	}
	if hasSummaries {
		header += "\tWHAT CHANGED"
	}
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		line := fmt.Sprintf("%d\t%s\t%s", r.Revision, r.SHA256[:12], dateOf(r.RevisedAt))
		if hasSummaries {
			line += "\t" + r.DiffSummary
		}
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
}

// RenderProjects renders `taskr project ls`. It exists to answer one
// question — does a project already cover where I'm standing — so each
// project's repos and dirs are shown alongside its identity, not just its
// slug and key.
func RenderProjects(w io.Writer, rows []ProjectView) {
	for _, v := range rows {
		fmt.Fprintf(w, "%s  (%s)  key=%s\n", v.Slug, v.Name, v.Key)
		for _, r := range v.Repos {
			fmt.Fprintf(w, "  repo  %s\n", r.RemoteURL)
		}
		for _, d := range v.Dirs {
			fmt.Fprintf(w, "  dir   %s  (%s)\n", d.Subpath, d.RemoteURL)
		}
		renderConventions(w, "  ", v.Conventions)
	}
}

// renderConventions prints the conventions a project has actually recorded
// and nothing for the ones it has not. An unset convention is not "empty",
// it is unanswered, and printing "pr target:" with nothing after it invites
// an agent to treat the blank as the answer (TSK-111).
func renderConventions(w io.Writer, indent string, c ProjectConventions) {
	for _, row := range []struct{ label, value string }{
		{"branch", c.BranchFormat},
		{"commit", c.CommitStyle},
		{"pr into", c.PRTarget},
	} {
		if strings.TrimSpace(row.value) != "" {
			fmt.Fprintf(w, "%s%-7s %s\n", indent, row.label, row.value)
		}
	}
}

// stepGlyph is the one-character status vocabulary `step ls` prints per
// row. The codebase's one existing glyph pair — ○/✓ on a group walk — is a
// binary open/closed view and does not stretch to a step's six statuses,
// so this defines its own rather than force-fitting that one. Plain ASCII,
// not Unicode, so a plain terminal or a log file renders it the same way.
func stepGlyph(status string) string {
	switch status {
	case "pending":
		return "."
	case "in_progress":
		return ">"
	case "done":
		return "x"
	case "dropped":
		return "~"
	case "promoted":
		return "^"
	case "abandoned":
		return "-"
	default:
		return "?"
	}
}

// latestSHA returns the most recent head SHA a step's marks recorded, or
// "" if none carried one — a mark's snapshot is only ever sent when the
// caller had a TASKR_HEAD to report.
func latestSHA(marks []MarkView) string {
	for i := len(marks) - 1; i >= 0; i-- {
		if marks[i].HeadSHA != "" {
			return marks[i].HeadSHA
		}
	}
	return ""
}

// stepNote is the one extra fact worth a column of its own: the SHA a step
// in progress was last marked at, or what a step that left the plan became
// or why. Every other status has nothing more to say.
func stepNote(s StepView) string {
	switch s.Status {
	case "in_progress":
		if sha := latestSHA(s.Marks); sha != "" {
			return "since " + sha
		}
	case "dropped":
		if s.DropReason != "" {
			return "dropped: " + s.DropReason
		}
		return "dropped"
	case "promoted":
		ref := s.PromotedRef
		if ref == "" {
			ref = s.PromotedTo
		}
		if ref != "" {
			return "promoted -> " + ref
		}
		return "promoted"
	}
	return ""
}

// renderStepRows writes one line per step — position, status glyph, title,
// and stepNote's one extra fact — or the empty-plan line when there are
// none. Shared by RenderSteps (`step ls`, with a progress header ahead of
// it) and `step drop`'s output (the plan as it stands right after the
// drop; that response carries no step_progress to head it with).
func renderStepRows(w io.Writer, steps []StepView) {
	if len(steps) == 0 {
		fmt.Fprintln(w, "  no steps yet — `taskr step add` to start the plan")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, s := range steps {
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", s.Position, stepGlyph(s.Status), s.Title, stepNote(s))
	}
	tw.Flush()
}

// RenderSteps renders `taskr step ls`: this is what a person and a cold
// agent both read to see where the work stands, so the header names the
// issue and leads with how much of the plan is done — read straight off
// issue.StepProgress, computed server-side (internal/app/step.go's
// StepProgress), rather than recomputed here. The counting rule (which
// statuses count on which side of the fraction) should exist once, on the
// server; a client-side copy is a client that can silently disagree with
// it the day that rule changes. StepProgress is nil for an issue with no
// steps, in which case the header names just the issue and renderStepRows
// prints the empty-plan line.
func RenderSteps(w io.Writer, issue IssueView) {
	fmt.Fprintf(w, "%s — %s", issue.Ref, issue.Title)
	if p := issue.StepProgress; p != nil {
		fmt.Fprintf(w, "   %d/%d done", p.Done, p.Total)
	}
	fmt.Fprintln(w)
	renderStepRows(w, issue.Steps)
}

func indent(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ")
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// branchOrDetached names an empty branch rather than leaving a hole in the
// line. A snapshot taken on a detached HEAD carries no branch, and the head
// is worth recording anyway (see gitSnapshot) — "repo @  sha" would read as
// a rendering bug, where "(detached)" reads as the fact it is.
func branchOrDetached(branch string) string {
	if strings.TrimSpace(branch) == "" {
		return "(detached)"
	}
	return branch
}

// RenderSimilar prints semantic suggestions beside some issue (TSK-167):
// score, ref, title, then the action that records a confirmation — hint
// names the command for the surface being rendered ("taskr new" suggests a
// relate; triage suggests the duplicate verdict). Silent when empty:
// feature off, embedder degraded, or genuinely nothing alike.
func RenderSimilar(w io.Writer, similar []Neighbor, hint string) {
	if len(similar) == 0 {
		return
	}
	fmt.Fprintf(w, "Similar open issues:\n")
	for _, n := range similar {
		fmt.Fprintf(w, "  %.2f  %-9s %s\n", n.Score, n.Ref, n.Title)
	}
	if hint != "" {
		fmt.Fprintf(w, "%s\n", hint)
	}
}

// RenderCatchup prints `taskr catchup` for a human.
//
// Dead ends come first, before the state line and before the plan, which
// is the same order the server fills the packet in and for the same
// reason: they are the lines that stop an agent re-walking an approach a
// previous session already paid to rule out. Everything else here is
// context for them.
func RenderCatchup(w io.Writer, p CatchupPacket) {
	fmt.Fprintf(w, "%s — %s\n", p.Ref, p.Title)
	renderCatchupState(w, p.State)

	renderDeadEnds(w, p.DeadEnds)

	// After the dead ends and before the plan, matching the order the
	// server fills them in: what not to retry outranks what the task is.
	if p.Description != "" {
		fmt.Fprintf(w, "\n%s\n", indent(p.Description))
	}

	if len(p.Plan) > 0 {
		fmt.Fprintf(w, "\nStill to do:\n")
		for _, l := range p.Plan {
			marker := "○"
			if l.Status == "in_progress" {
				marker = "▸"
			}
			fmt.Fprintf(w, "  %d. %s %s\n", l.Position, marker, l.Title)
			if l.Note != "" {
				fmt.Fprintf(w, "        %s\n", indent(l.Note))
			}
		}
	}

	renderDigests(w, p.History)

	if len(p.Evidence) > 0 {
		// Printed as the git command rather than as two hashes: the point
		// of shipping addresses instead of diffs is that the reader runs
		// the diff, and a line they can paste is the shortest path to that.
		// Command first, provenance behind a `#`, so the whole line pastes
		// into a shell. Shipping addresses instead of diffs only pays off
		// if the address is one paste from being an answer.
		fmt.Fprintf(w, "\nWhat moved (run these yourself):\n")
		for _, s := range p.Evidence {
			fmt.Fprintf(w, "  git log %s..%s   # %s, %s\n", s.From, s.To, s.Repo, branchOrDetached(s.Branch))
		}
	}

	renderTrail(w, p.Trail)

	if p.Budget.Notice != "" {
		fmt.Fprintf(w, "\n%s\n", p.Budget.Notice)
	}
}

func renderCatchupState(w io.Writer, s CatchupState) {
	fmt.Fprintf(w, "status: %s   priority: %s   kind: %s", s.Status, s.Priority, s.Kind)
	if s.Progress != "" {
		fmt.Fprintf(w, "   plan: %s", s.Progress)
	}
	fmt.Fprintln(w)
	if s.HeadSHA != "" {
		fmt.Fprintf(w, "last seen on %s @ %s\n", branchOrDetached(s.Branch), s.HeadSHA)
	}
	if s.Resolution != "" {
		fmt.Fprintf(w, "resolution: %s\n", s.Resolution)
	}
}

// renderDeadEnds is shared by `taskr catchup` and the resume packet, so the
// most load-bearing block in either surface reads identically in both.
func renderDeadEnds(w io.Writer, ends []DeadEnd) {
	if len(ends) == 0 {
		return
	}
	fmt.Fprintf(w, "\nAlready ruled out — do not retry %s:\n", plural(len(ends), "this", "these"))
	for _, d := range ends {
		what := d.Step
		if what == "" {
			what = deadEndKind(d.Kind)
		}
		fmt.Fprintf(w, "  ✗ %s\n", what)
		if d.Reason != "" {
			fmt.Fprintf(w, "      %s\n", indent(d.Reason))
		}
		if d.HeadSHA != "" {
			fmt.Fprintf(w, "      at %s\n", d.HeadSHA)
		}
	}
}

// deadEndKind names a dead end that is not a step, so the line says what it
// was rather than rendering blank.
func deadEndKind(kind string) string {
	switch kind {
	case "reopened":
		return "this was closed once and reopened"
	case "dropped":
		return "a dropped step"
	case "ruled_out":
		return "a ruled-out step"
	}
	return kind
}

func renderDigests(w io.Writer, rows []SessionDigest) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "\nHow it got here:\n")
	for _, d := range rows {
		when := d.Date
		if d.Through != "" {
			when = fmt.Sprintf("%s..%s", d.Date, d.Through)
		}
		fmt.Fprintf(w, "  %s  %s", when, d.Summary)
		if d.Sessions > 1 {
			fmt.Fprintf(w, " (×%d sessions)", d.Sessions)
		}
		if d.HeadSHA != "" {
			fmt.Fprintf(w, "  @ %s", d.HeadSHA)
		}
		fmt.Fprintln(w)
	}
}

// renderTrail prints the --deep layer. It is the one part of a catch-up
// that carries people's actual words, so it renders them whole rather than
// truncating: a caller who asked for the decision trail asked for what was
// said, and half a sentence is worse than a pointer.
func renderTrail(w io.Writer, t *DecisionTrail) {
	if t == nil {
		return
	}
	for _, v := range t.Verdicts {
		fmt.Fprintf(w, "\nTriaged %s", v.Verdict)
		if v.DuplicateOf != "" {
			fmt.Fprintf(w, " of %s", v.DuplicateOf)
		}
		fmt.Fprintf(w, " (%s)\n", dateOf(v.At))
		if v.Evidence != "" {
			fmt.Fprintf(w, "  %s\n", indent(v.Evidence))
		}
	}
	if len(t.Checks) > 0 {
		fmt.Fprintf(w, "\nChecks:\n")
		for _, c := range t.Checks {
			fmt.Fprintf(w, "  [%s] %s\n", c.Status, c.Title)
			if c.Expect != "" {
				fmt.Fprintf(w, "      expect: %s\n", indent(c.Expect))
			}
		}
	}
	if len(t.Documents) > 0 {
		fmt.Fprintf(w, "\nDocuments (read with `taskr doc show <id>`):\n")
		for _, d := range t.Documents {
			fmt.Fprintf(w, "  [%s] %s (%s)\n", d.Type, d.Title, d.ID)
		}
	}
	if len(t.Comments) > 0 {
		fmt.Fprintf(w, "\nComments:\n")
		for _, c := range t.Comments {
			fmt.Fprintf(w, "  %s (%s): %s\n", c.Actor, dateOf(c.CreatedAt), indent(c.Body))
		}
	}
}

// renderCatchupSection prints the catch-up that rides inside a resume
// packet. It is deliberately shorter than `taskr catchup`: the packet
// already prints the status, the tree state and the plan above it, so
// repeating them would spend a resuming agent's tokens saying the same
// thing twice.
func renderCatchupSection(b *strings.Builder, c *CatchupSection) {
	if c == nil {
		return
	}
	renderDeadEnds(b, c.DeadEnds)
	renderDigests(b, c.History)
	if c.Budget.Notice != "" {
		fmt.Fprintf(b, "\n%s\n", c.Budget.Notice)
	}
	b.WriteString("\n")
}
