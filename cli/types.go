// cli/types.go
package cli

// The types below mirror the JSON shapes internal/api actually writes on
// the wire (see internal/app for the source of truth). They are redefined
// here, deliberately, rather than imported from internal/app: the CLI is an
// HTTP client and nothing else, and importing internal/app would pull the
// store and domain packages — and a database driver — into a binary that
// never opens one.

// IssueRef identifies an issue by both its stable id and its human
// reference, e.g. "TSK-12".
type IssueRef struct {
	ID  string `json:"id"`
	Ref string `json:"ref"`
}

// DocumentRef names a document without its body.
type DocumentRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type CommentView struct {
	ID        string `json:"id"`
	Actor     string `json:"actor"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type AgentContextEntry struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
	SessionID string `json:"session_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

type SnapshotView struct {
	Repo       string   `json:"repo"`
	Branch     string   `json:"branch"`
	HeadSHA    string   `json:"head_sha"`
	Worktree   string   `json:"worktree,omitempty"`
	MergeBase  string   `json:"merge_base,omitempty"`
	DirtyFiles []string `json:"dirty_files,omitempty"`
	RecordedAt string   `json:"recorded_at"`
}

// GroupChild is one child issue within a group, as it appears in a parent's
// GroupBlock or in a GroupHint's NextChild.
type GroupChild struct {
	ID       string `json:"id"`
	Ref      string `json:"ref"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Position int    `json:"position"`
}

// GroupBlock is the children side of an issue view: present only when the
// issue is itself a parent.
type GroupBlock struct {
	Children  []GroupChild `json:"children"`
	NextChild *GroupChild  `json:"next_child,omitempty"`
	Closed    int          `json:"closed"`
	Total     int          `json:"total"`
}

// ParentBlock is the parent side of an issue view: present only when the
// issue is itself a child.
type ParentBlock struct {
	ID     string `json:"id"`
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Closed int    `json:"closed"`
	Total  int    `json:"total"`
}

// GroupHint rides along a close response when the closed issue belongs to a
// group — enough to point an agent at what to work on next without a second
// round trip.
type GroupHint struct {
	ParentRef         string      `json:"parent_ref"`
	NextChild         *GroupChild `json:"next_child,omitempty"`
	AllChildrenClosed bool        `json:"all_children_closed,omitempty"`
}

// UpdateIssueResult is the response from PATCH /api/issues/{ref}.
//
// AbandonedSteps is every step the close left unfinished, in plan order.
// Steps never gate a close, so this is the only moment the caller is told
// what the plan did not reach — to name it in the resolution, or to offload
// it. The server has always sent it; this client used to decode the close
// response without the field and drop it on the floor (TSK-113).
type UpdateIssueResult struct {
	ID             string      `json:"id"`
	Ref            string      `json:"ref"`
	GroupHint      *GroupHint  `json:"group_hint,omitempty"`
	AbandonedSteps []StepBrief `json:"abandoned_steps,omitempty"`
}

// IssueView is the read model for one issue.
type IssueView struct {
	ID           string              `json:"id"`
	Ref          string              `json:"ref"`
	ProjectID    string              `json:"project_id"`
	Title        string              `json:"title"`
	Description  string              `json:"description"`
	Kind         string              `json:"kind"`
	Status       string              `json:"status"`
	Priority     string              `json:"priority"`
	ManualRank   int                 `json:"manual_rank"`
	Resolution   string              `json:"resolution,omitempty"`
	CreatedAt    string              `json:"created_at"`
	UpdatedAt    string              `json:"updated_at"`
	Comments     []CommentView       `json:"comments,omitempty"`
	Snapshot     *SnapshotView       `json:"git_snapshot,omitempty"`
	AgentContext []AgentContextEntry `json:"agent_context,omitempty"`
	Group        *GroupBlock         `json:"group,omitempty"`
	Parent       *ParentBlock        `json:"parent,omitempty"`
	Checks       []CheckView         `json:"checks,omitempty"`
	// Steps is the issue's working plan in order, and StepProgress the
	// one-line summary of it — both computed server-side (internal/app/
	// step.go's StepProgress), so `step ls` reads the count from here
	// rather than re-deriving it: the counting rule (which statuses count
	// on which side of the fraction) should exist once, not mirrored in
	// this client and left to drift if the server's ever changes.
	Steps        []StepView    `json:"steps,omitempty"`
	StepProgress *StepProgress `json:"step_progress,omitempty"`
}

// SearchResult is the compact row `ls` renders. It never carries the
// agent-context layer.
type SearchResult struct {
	ID          string `json:"id"`
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Kind        string `json:"kind"`
	Priority    string `json:"priority"`
	ProjectSlug string `json:"project_slug"`
	UpdatedAt   string `json:"updated_at"`
}

// RelatedIssue is one neighbour in an issue's graph.
type RelatedIssue struct {
	ID     string `json:"id"`
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Type   string `json:"type"`
	Depth  int    `json:"depth"`
}

// GraphContext is the neighbourhood around one issue.
type GraphContext struct {
	IssueID          string         `json:"issue_id"`
	Blocks           []RelatedIssue `json:"blocks"`
	BlockedBy        []RelatedIssue `json:"blocked_by"`
	RelatesTo        []RelatedIssue `json:"relates_to"`
	DiscoveredDuring []RelatedIssue `json:"discovered_during"`
	Discovered       []RelatedIssue `json:"discovered"`
	Children         []RelatedIssue `json:"children,omitempty"`
	Parent           []RelatedIssue `json:"parent,omitempty"`
}

// SessionView is one work session.
type SessionView struct {
	ID             string `json:"id"`
	Machine        string `json:"machine"`
	ProjectID      string `json:"project_id,omitempty"`
	IssueID        string `json:"issue_id,omitempty"`
	Agent          string `json:"agent"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	Status         string `json:"status"`
	StartedAt      string `json:"started_at"`
	ResumeCommand  string `json:"resume_command,omitempty"`
}

// ParkView is why work stopped, and what to do next.
type ParkView struct {
	SessionID     string `json:"session_id"`
	IssueID       string `json:"issue_id,omitempty"`
	Machine       string `json:"machine,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	Reason        string `json:"reason"`
	ResumeNote    string `json:"resume_note,omitempty"`
	ParkedAt      string `json:"parked_at"`
	ResumeCommand string `json:"resume_command,omitempty"`
}

// ResumePacket is everything a cold agent needs in a single response — the
// product of `taskr start`.
type ResumePacket struct {
	Session       SessionView   `json:"session"`
	Issue         IssueView     `json:"issue"`
	Graph         GraphContext  `json:"graph"`
	LastPark      *ParkView     `json:"last_park,omitempty"`
	PriorSessions []SessionView `json:"prior_sessions,omitempty"`
	Documents     []DocumentRef `json:"documents,omitempty"`
}

// ProjectConventions carries a project's branch, commit, and PR conventions.
type ProjectConventions struct {
	BranchFormat string `json:"branch_format,omitempty"`
	CommitStyle  string `json:"commit_style,omitempty"`
	PRTarget     string `json:"pr_target,omitempty"`
}

// RepoRef is the read model for one repo.
type RepoRef struct {
	ID            string `json:"id"`
	RemoteURL     string `json:"remote_url"`
	Host          string `json:"host"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// DirRef is one repo-relative directory a project has claimed.
type DirRef struct {
	RemoteURL string `json:"remote_url"`
	Subpath   string `json:"subpath"`
}

// ProjectView is the read model for one project.
type ProjectView struct {
	ID          string             `json:"id"`
	Slug        string             `json:"slug"`
	Name        string             `json:"name"`
	Key         string             `json:"key"`
	Description string             `json:"description,omitempty"`
	Conventions ProjectConventions `json:"conventions"`
	CreatedAt   string             `json:"created_at"`
	Repos       []RepoRef          `json:"repos,omitempty"`
	Dirs        []DirRef           `json:"dirs,omitempty"`
}

// SetupHint tells a caller what to collect so it can register a repo taskr
// has never seen. taskr never runs git itself; a human or agent gathers
// these and calls setup_project (over MCP or the API) with the results.
type SetupHint struct {
	Reason  string   `json:"reason"`
	Collect []string `json:"collect"`
}

// AmbiguousHint is a sibling to SetupHint: the repo IS registered, but it
// serves more than one project and the caller's directory doesn't pick one
// out — the same case a write refuses on. Reason names the candidates, the
// same way that refusal does.
type AmbiguousHint struct {
	Reason string `json:"reason"`
}

// ContextView answers "where am I and what was I doing" — the response to
// `taskr context`.
type ContextView struct {
	Machine       string         `json:"machine"`
	ActiveSession *SessionView   `json:"active_session,omitempty"`
	ActiveIssue   *IssueView     `json:"active_issue,omitempty"`
	Parked        []SessionView  `json:"parked,omitempty"`
	OpenIssues    int            `json:"open_issues"`
	Project       *ProjectView   `json:"project,omitempty"`
	SetupHint     *SetupHint     `json:"setup_hint,omitempty"`
	Ambiguous     *AmbiguousHint `json:"ambiguous,omitempty"`
}

// Candidate is one issue in the ranked ready pool, with the reasons behind
// its score.
type Candidate struct {
	IssueID  string   `json:"issue_id"`
	Ref      string   `json:"ref"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Priority string   `json:"priority"`
	Score    float64  `json:"score"`
	Reasons  []string `json:"reasons"`
}

// TimelineEntry is one event in an issue's history.
type TimelineEntry struct {
	GlobalSeq  int64  `json:"global_seq"`
	StreamSeq  int    `json:"stream_seq"`
	Type       string `json:"type"`
	Actor      string `json:"actor"`
	SessionID  string `json:"session_id,omitempty"`
	OccurredAt string `json:"occurred_at"`
	Summary    string `json:"summary,omitempty"`
}

// OffloadResult is deliberately small: enough to mention the new issue and
// keep going.
type OffloadResult struct {
	Issue         IssueRef `json:"issue"`
	ParentIssueID string   `json:"parent_issue_id,omitempty"`
}

// authStatus is the response from GET /api/auth/status.
//
// Actor is who this credential's writes are recorded as, and it is the
// point of the endpoint for anyone but the SPA: authorship comes entirely
// from the key, so nothing a request body claims can change it. Absent
// means unauthenticated — no credential, therefore no actor, rather than a
// default worth printing.
type authStatus struct {
	Authenticated bool     `json:"authenticated"`
	Required      bool     `json:"required"`
	Actor         string   `json:"actor,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	KeyID         string   `json:"key_id,omitempty"`
	Billing       *struct {
		Status      string `json:"status"`
		Writable    bool   `json:"writable"`
		TrialEndsAt string `json:"trial_ends_at,omitempty"`
	} `json:"billing,omitempty"`
}

// --- Checks (TSK-90) ---

// Measurement is one typed number a check run records.
type Measurement struct {
	Metric     string  `json:"metric"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit,omitempty"`
	Conditions string  `json:"conditions,omitempty"`
}

// DefineCheckInput is POST /api/issues/{ref}/checks' body.
type DefineCheckInput struct {
	Title     string `json:"title"`
	Procedure string `json:"procedure,omitempty"`
	Expect    string `json:"expect,omitempty"`
	Runner    string `json:"runner,omitempty"`
}

// CheckRef names a created check and the issue it belongs to.
type CheckRef struct {
	ID    string   `json:"id"`
	Issue IssueRef `json:"issue"`
}

// RunView is one recorded run.
type RunView struct {
	ID            string        `json:"id"`
	Outcome       string        `json:"outcome"`
	Measurements  []Measurement `json:"measurements,omitempty"`
	EvidenceDocID string        `json:"evidence_doc_id,omitempty"`
	HeadSHA       string        `json:"head_sha,omitempty"`
	Image         string        `json:"image,omitempty"`
	Note          string        `json:"note,omitempty"`
	Actor         string        `json:"actor,omitempty"`
	RecordedAt    string        `json:"recorded_at,omitempty"`
}

// RunCheckInput is POST /api/checks/{id}/runs' body.
type RunCheckInput struct {
	Outcome       string        `json:"outcome"`
	Measurements  []Measurement `json:"measurements,omitempty"`
	EvidenceDocID string        `json:"evidence_doc_id,omitempty"`
	HeadSHA       string        `json:"head_sha,omitempty"`
	Image         string        `json:"image,omitempty"`
	Note          string        `json:"note,omitempty"`
}

// CheckView is one check with its latest run.
type CheckView struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Procedure string   `json:"procedure,omitempty"`
	Expect    string   `json:"expect,omitempty"`
	Runner    string   `json:"runner"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"created_at"`
	LatestRun *RunView `json:"latest_run,omitempty"`
}

// PendingCheck is one check waiting to pass, with its issue.
type PendingCheck struct {
	IssueID    string `json:"issue_id"`
	IssueRef   string `json:"issue_ref"`
	IssueTitle string `json:"issue_title"`
	CheckID    string `json:"check_id"`
	Title      string `json:"title"`
	Runner     string `json:"runner"`
}

// PendingChecksBody is the 409 a close over pending checks answers with.
type PendingChecksBody struct {
	Error         string             `json:"error"`
	PendingChecks []PendingCheckItem `json:"pending_checks"`
}

// PendingCheckItem is one pending check as the 409 names it.
type PendingCheckItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Runner string `json:"runner"`
}

// --- Steps (TSK-105) ---

// StepRef names a created step and the issue it belongs to.
type StepRef struct {
	ID    string   `json:"id"`
	Issue IssueRef `json:"issue"`
}

// MarkView is one recorded transition on a step — a start or a done, with
// whatever the caller reported about the tree at that moment.
type MarkView struct {
	Kind       string `json:"kind"`
	Note       string `json:"note,omitempty"`
	Branch     string `json:"branch,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
	DirtyCount int    `json:"dirty_count,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Actor      string `json:"actor"`
	RecordedAt string `json:"recorded_at"`
}

// StepView is one step in an issue's ordered working plan.
type StepView struct {
	ID           string     `json:"id"`
	Position     int        `json:"position"`
	Title        string     `json:"title"`
	Body         string     `json:"body,omitempty"`
	Status       string     `json:"status"`
	PromotedKind string     `json:"promoted_kind,omitempty"`
	PromotedTo   string     `json:"promoted_to,omitempty"`
	PromotedRef  string     `json:"promoted_ref,omitempty"`
	DropReason   string     `json:"drop_reason,omitempty"`
	HeldBy       string     `json:"held_by,omitempty"`
	StartedAt    string     `json:"started_at,omitempty"`
	EndedAt      string     `json:"ended_at,omitempty"`
	Marks        []MarkView `json:"marks,omitempty"`
}

// StepBrief is a step small enough to embed in a summary — StepProgress's
// Current and Next.
type StepBrief struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Title    string `json:"title"`
	Status   string `json:"status"`
}

// StepProgress is the one-line summary of an issue's plan, computed
// server-side and read off IssueView rather than recomputed here: Total
// counts steps still part of the plan (done, pending, in progress) —
// dropped, promoted and abandoned ones leave BOTH sides of the fraction,
// so a plan that shed steps reads e.g. 2/2 rather than 2/4. Mirrors
// internal/app/step.go's StepProgress.
type StepProgress struct {
	Done    int        `json:"done"`
	Total   int        `json:"total"`
	Current *StepBrief `json:"current,omitempty"`
	Next    *StepBrief `json:"next,omitempty"`
}

// AddStepsInput is POST /api/issues/{ref}/steps' body. Body is only
// meaningful when Titles has a single entry — the server ignores it
// otherwise, the same rule AddSteps documents server-side.
type AddStepsInput struct {
	Titles    []string `json:"titles"`
	Body      string   `json:"body,omitempty"`
	After     string   `json:"after,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
}

// EditStepInput is PATCH /api/steps/{id}'s body. Title and Body are
// *string so an unset field leaves the step untouched server-side; an
// explicitly empty title is refused with 400 rather than accepted as a
// clear.
type EditStepInput struct {
	Title     *string `json:"title,omitempty"`
	Body      *string `json:"body,omitempty"`
	SessionID string  `json:"session_id,omitempty"`
}

// MoveStepInput is PUT /api/steps/{id}/position's body. After is a step
// id, never a position — the CLI resolves a position selector before
// building this. No omitempty on After: an empty string is how "move to
// the front" is spelled on the wire, and it must be sent, not dropped.
type MoveStepInput struct {
	After     string `json:"after"`
	SessionID string `json:"session_id,omitempty"`
}

// StepSnapshot is the git snapshot a start or done mark carries. taskr
// never runs git, and neither does this client: it only ever reads
// TASKR_HEAD, so HeadSHA is the one field it ever sets. There is no
// TASKR_BRANCH or TASKR_DIRTY — branch and dirty-count stay empty from
// this client, left for whatever else records a mark to fill in.
type StepSnapshot struct {
	HeadSHA string `json:"head_sha,omitempty"`
}

// StepMarkInput is POST /api/steps/{id}/start and .../done's body.
type StepMarkInput struct {
	Note      string        `json:"note,omitempty"`
	Snapshot  *StepSnapshot `json:"git_snapshot,omitempty"`
	SessionID string        `json:"session_id,omitempty"`
}

// StepStatusResult is the response from start and done: just the status
// the step landed on, so a caller can confirm the transition took without
// a second read.
type StepStatusResult struct {
	Status string `json:"status"`
}

// DropStepInput is POST /api/steps/{id}/drop's body.
type DropStepInput struct {
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// PromoteStepInput is POST /api/steps/{id}/promote's body. Block is a
// *bool: nil omits it from the wire so the server's own default (true,
// for a child issue) applies; only --no-block sets it, to false.
type PromoteStepInput struct {
	Became    string `json:"became,omitempty"`
	Block     *bool  `json:"block,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Title     string `json:"title,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// PromoteResult names what a step became.
type PromoteResult struct {
	StepID    string `json:"step_id"`
	Became    string `json:"became"`
	TargetID  string `json:"target_id"`
	TargetRef string `json:"target_ref,omitempty"`
	Blocked   bool   `json:"blocked"`
}
