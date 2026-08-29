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

// Neighbor is one semantic suggestion from the duplicate gate (TSK-167):
// an open issue whose text resembles the one just filed, with nothing
// already connecting the two. Score is cosine similarity; comparable only
// to other Neighbor scores.
type Neighbor struct {
	ID      string  `json:"id"`
	Ref     string  `json:"ref"`
	Title   string  `json:"title"`
	Status  string  `json:"status"`
	Project string  `json:"project,omitempty"`
	Score   float64 `json:"score"`
}

// CreatedIssue is POST /api/issues' response: the ref plus, when the
// server's semantic feature is on and matches exist, the duplicate gate's
// suggestions. Embedding IssueRef keeps every existing caller compiling.
type CreatedIssue struct {
	IssueRef
	Similar []Neighbor `json:"similar,omitempty"`
}

// CreatedOffload is POST /api/offload's response, same shape reasoning as
// CreatedIssue.
type CreatedOffload struct {
	OffloadResult
	Similar []Neighbor `json:"similar,omitempty"`
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
	// NeverStarted is how many of AbandonedSteps were still pending when
	// the close abandoned them — never started, let alone finished. The
	// abandoned list alone cannot say this, since every entry in it reads
	// "abandoned" by the time the caller sees it (TSK-139).
	NeverStarted int `json:"never_started,omitempty"`
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
// agent-context layer. The search server may additionally send layer, score
// and evidence fields (TSK-137's layered agent search); they decode here and
// pass through --json untouched, but the human table does not render them.
type SearchResult struct {
	ID          string  `json:"id"`
	Ref         string  `json:"ref"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	Kind        string  `json:"kind"`
	Priority    string  `json:"priority"`
	ProjectSlug string  `json:"project_slug"`
	UpdatedAt   string  `json:"updated_at"`
	Rot         bool    `json:"rot,omitempty"`
	Layer       string  `json:"layer,omitempty"`
	Score       float64 `json:"score,omitempty"`
	Evidence    string  `json:"ev,omitempty"`
}

// AgentSearchVersion versions the `taskr ls --json` envelope shape. An agent
// branches on it rather than guessing what it is holding; bump when a field
// changes meaning.
const AgentSearchVersion = 1

// AgentSearchResponse is the machine-facing search envelope: per-layer hit
// counts (zeros present, never omitted), corpus spelling suggestions when
// the exact layer missed, ready-to-append widen filters, and a More flag so
// truncation is never silent. Zero results is information, not absence:
// didyoumean plus layers tells the caller whether to retry broader or give
// up honestly.
type AgentSearchResponse struct {
	Q          string         `json:"q"`
	Layers     map[string]int `json:"layers"`
	DidYouMean []string       `json:"didyoumean,omitempty"`
	Expand     []string       `json:"expand,omitempty"`
	Results    []SearchResult `json:"results"`
	More       bool           `json:"more"`
	V          int            `json:"v"`
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
	// The fields below describe the park this row stands for, and are
	// filled only for the parked list — the server sends them additively on
	// the same type (no coordinated release), and a row rendered anywhere
	// else has no park to describe.
	IssueRef   string `json:"issue_ref,omitempty"`
	IssueTitle string `json:"issue_title,omitempty"`
	Reason     string `json:"reason,omitempty"`
	ResumeNote string `json:"resume_note,omitempty"`
	ParkedAt   string `json:"parked_at,omitempty"`
	// AlsoParked counts the other sessions parked on this same issue; the
	// list is keyed by issue, so this is how the older parks stay visible.
	AlsoParked int `json:"also_parked,omitempty"`
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
	// Auto says the harness parked this on the way out — nobody decided to
	// stop, and the note under it was composed from the tree rather than
	// written. It changes how the note should be read, so `start` says so
	// before printing it.
	Auto bool `json:"auto,omitempty"`
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
	// Catchup is how the work got here: the approaches already ruled out
	// and a collapsed history of the sessions that did it.
	Catchup *CatchupSection `json:"catchup,omitempty"`
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
	Machine       string       `json:"machine"`
	ActiveSession *SessionView `json:"active_session,omitempty"`
	ActiveIssue   *IssueView   `json:"active_issue,omitempty"`
	// UntouchedPlan is set when ActiveIssue has steps still open and none
	// has moved since ActiveSession started. See its type.
	UntouchedPlan *UntouchedPlan `json:"untouched_plan,omitempty"`
	Parked        []SessionView  `json:"parked,omitempty"`
	OpenIssues    int            `json:"open_issues"`
	Project       *ProjectView   `json:"project,omitempty"`
	SetupHint     *SetupHint     `json:"setup_hint,omitempty"`
	Ambiguous     *AmbiguousHint `json:"ambiguous,omitempty"`
}

// UntouchedPlan is the server's report that a plan is not being kept: the
// active session's issue has Open steps still pending or in progress, and
// no step has been started or finished since the session began at Since.
// An unmoved plan makes every summary lie to the next reader — 0/11 for
// work that is 3/11 done — so it is said out loud at orientation and again
// at park, before the session that could still fix it is gone (TSK-139).
type UntouchedPlan struct {
	IssueRef string `json:"issue_ref"`
	Open     int    `json:"open"`
	Since    string `json:"since"`
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

// TriageCandidate is one open issue that needs a fresh verdict, and why.
// Reason is new (never triaged), rot (the repo moved under the issue's
// snapshot: SnapshotSHA against LatestSHA), or expired (its verdict aged
// out; TriagedAt is when that verdict was recorded).
type TriageCandidate struct {
	IssueID     string `json:"issue_id"`
	IssueRef    string `json:"issue_ref"`
	Title       string `json:"title"`
	Reason      string `json:"reason"`
	SnapshotSHA string `json:"snapshot_sha,omitempty"`
	LatestSHA   string `json:"latest_sha,omitempty"`
	TriagedAt   string `json:"triaged_at,omitempty"`
	// Twin is the closest open issue in the same project with no edge
	// between the two, when one scores at or above the duplicate bar —
	// the duplicate net at scan time (TSK-178). Absent when the server's
	// embedding feature is off or nothing comes close.
	Twin *Neighbor `json:"twin,omitempty"`
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

// StepMarkInput is POST /api/steps/{id}/start and .../done's body. The
// snapshot is the same GitSnapshotInput new, offload and park send — a
// mark is a write like any other, and internal/projection/steps.go's
// writeMark already stores branch and dirty_count into step_marks, so
// there is no narrower shape to carry here.
type StepMarkInput struct {
	Note      string            `json:"note,omitempty"`
	Snapshot  *GitSnapshotInput `json:"git_snapshot,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
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

// --- Catch-up (TSK-212) ---
//
// The wire shapes of GET /api/issues/{ref}/catchup. They mirror
// internal/app/catchup.go on the server; the CLI decodes and renders, and
// derives nothing of its own — a client that recomputed any of this would
// be a second place for the elision rules to drift.

// CatchupPacket is how an issue got from 0 to now, under a token budget.
type CatchupPacket struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
	// Description is the issue's brief. It is what lets a catch-up be
	// started from on its own, instead of alongside `taskr show`.
	Description string          `json:"description,omitempty"`
	DeadEnds    []DeadEnd       `json:"dead_ends,omitempty"`
	Plan        []PlanLine      `json:"plan,omitempty"`
	State       CatchupState    `json:"state"`
	History     []SessionDigest `json:"history,omitempty"`
	Evidence    []Span          `json:"evidence,omitempty"`
	Trail       *DecisionTrail  `json:"trail,omitempty"`
	Budget      BudgetReport    `json:"budget"`
}

// CatchupSection is the catch-up as it rides inside a resume packet: only
// the parts the packet does not already carry in fuller form.
type CatchupSection struct {
	DeadEnds []DeadEnd       `json:"dead_ends,omitempty"`
	History  []SessionDigest `json:"history,omitempty"`
	Budget   BudgetReport    `json:"budget"`
}

// CatchupState is where the issue stands right now. Branch and HeadSHA are
// the LAST commit anyone recorded against it, which is not the same as the
// issue's own snapshot and is the one a resuming agent should stand on.
type CatchupState struct {
	Status     string `json:"status"`
	Kind       string `json:"kind"`
	Priority   string `json:"priority"`
	Resolution string `json:"resolution,omitempty"`
	Progress   string `json:"progress,omitempty"`
	Branch     string `json:"branch,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
}

// DeadEnd is an approach the ledger already ruled out. Kind is "dropped",
// "ruled_out" or "reopened".
type DeadEnd struct {
	Kind    string `json:"kind"`
	Step    string `json:"step,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Actor   string `json:"actor,omitempty"`
	At      string `json:"at"`
	HeadSHA string `json:"head_sha,omitempty"`
}

// PlanLine is one step that has not finished.
type PlanLine struct {
	Position int    `json:"position"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Note     string `json:"note,omitempty"`
}

// SessionDigest is one session's work on one day, collapsed to a line.
// Through and Sessions are set when consecutive days that did the same
// thing were coalesced into one run.
type SessionDigest struct {
	SessionID string `json:"session_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Date      string `json:"date"`
	Through   string `json:"through,omitempty"`
	Sessions  int    `json:"sessions,omitempty"`
	Summary   string `json:"summary"`
	HeadSHA   string `json:"head_sha,omitempty"`
}

// Span is one stretch of work on one branch, as the two commits that
// bracket it. The server ships addresses and never diffs; git is local.
type Span struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch,omitempty"`
	From   string `json:"from"`
	To     string `json:"to"`
	At     string `json:"at"`
}

// DecisionTrail is the --deep layer: why the work went the way it did.
type DecisionTrail struct {
	Comments  []CommentView `json:"comments,omitempty"`
	Checks    []CheckView   `json:"checks,omitempty"`
	Documents []DocumentRef `json:"documents,omitempty"`
	Verdicts  []TriageNote  `json:"verdicts,omitempty"`
}

// TriageNote is one recorded verdict and the evidence behind it.
type TriageNote struct {
	Verdict     string `json:"verdict"`
	Evidence    string `json:"evidence,omitempty"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
	Actor       string `json:"actor,omitempty"`
	At          string `json:"at"`
}

// BudgetReport is what the packet cost and what it left out.
type BudgetReport struct {
	Budget         int    `json:"budget"`
	Estimated      int    `json:"estimated"`
	ElidedSessions int    `json:"elided_sessions,omitempty"`
	ElidedDeadEnds int    `json:"elided_dead_ends,omitempty"`
	ElidedSteps    int    `json:"elided_steps,omitempty"`
	ElidedSpans    int    `json:"elided_spans,omitempty"`
	ElidedTrail    int    `json:"elided_trail,omitempty"`
	Notice         string `json:"notice,omitempty"`
}
