// cli/render.go
package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
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

	if iv.Snapshot != nil {
		s := iv.Snapshot
		fmt.Fprintf(&b, "Tree state (as of %s):\n", s.RecordedAt)
		fmt.Fprintf(&b, "  %s @ %s  %s\n", s.Repo, s.Branch, s.HeadSHA)
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
	} else {
		b.WriteString("\nNo active session on this machine. Run `taskr next` or `taskr ls`, then `taskr start <ref>`.\n")
	}

	if len(v.Parked) > 0 {
		fmt.Fprintf(&b, "\nParked sessions (%d):\n", len(v.Parked))
		for _, s := range v.Parked {
			fmt.Fprintf(&b, "  %s on %s", s.ID, s.Machine)
			if s.IssueID != "" {
				fmt.Fprintf(&b, " (issue %s)", s.IssueID)
			}
			b.WriteString("\n")
		}
	}

	if v.Project != nil {
		fmt.Fprintf(&b, "\nProject: %s (%s)\n", v.Project.Name, v.Project.Slug)
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
		fmt.Fprintf(&b, "\ntree state: %s @ %s %s (as of %s)\n", s.Repo, s.Branch, s.HeadSHA, s.RecordedAt)
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
	}
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
