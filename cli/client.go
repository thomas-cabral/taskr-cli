// cli/client.go
package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client is a thin HTTP client over taskr's JSON API. It holds no domain
// logic — every method below is a direct call onto one endpoint, and the
// CLI never opens a database. That boundary is also why it never shells out
// to git: git state is something a caller reports, not something taskr (or
// this client) goes and gets.
type Client struct {
	BaseURL string // e.g. "http://127.0.0.1:8099"
	Key     string // sent unconditionally, empty or not
	HTTP    *http.Client
}

// APIError is a non-2xx response. Its Error() is the server's own message,
// verbatim — a 400 already names the legal values, and wrapping it would
// only make that harder to read.
type APIError struct {
	Status  int
	Message string
	// Body is the raw response body. Structured refusals — a close's 409
	// with pending_checks — carry more than one line, and serverMessage
	// keeps only the line; callers that can act on the structure decode
	// this instead.
	Body []byte
}

func (e *APIError) Error() string { return e.Message }

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// newIdempotencyKey returns a fresh retry-safe key for one write. Generated
// unconditionally on every write call: a CLI invocation is exactly one
// logical write, so there is never a reason to reuse a key across two.
func newIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// get issues a GET and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// write issues a POST or PATCH carrying reqBody as JSON, with a fresh
// Idempotency-Key, and decodes the response into out (when out is non-nil
// and the response carries a body — several writes answer 204).
func (c *Client) write(ctx context.Context, method, path string, reqBody, out any) error {
	body, err := c.doWrite(ctx, method, path, reqBody)
	if err != nil {
		return err
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *Client) doWrite(ctx context.Context, method, path string, reqBody any) ([]byte, error) {
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("taskr: encoding request: %w", err)
	}
	return c.doRaw(ctx, method, path, nil, buf, true)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, reqBody []byte) ([]byte, error) {
	return c.doRaw(ctx, method, path, query, reqBody, false)
}

func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values, reqBody []byte, isWrite bool) ([]byte, error) {
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reader io.Reader
	if reqBody != nil {
		reader = bytes.NewReader(reqBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if isWrite {
		req.Header.Set("Idempotency-Key", newIdempotencyKey())
	}
	// Sent unconditionally, empty or not: nothing about auth changes when a
	// host starts requiring a key.
	req.Header.Set("X-Taskr-Key", c.Key)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("taskr: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("taskr: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Message: serverMessage(resp.StatusCode, respBody), Body: respBody}
	}
	return respBody, nil
}

// serverMessage extracts the human-readable message from an error response.
// The auth endpoints answer with {"error": "..."}; every other failing
// write or read answers with http.Error's plain text (the domain's message
// plus a trailing newline). Either way the caller gets one clean line.
func serverMessage(status int, body []byte) string {
	var withError struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &withError) == nil && withError.Error != "" {
		return withError.Error
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return msg
	}
	return http.StatusText(status)
}

// --- Reads ---

func (c *Client) AuthStatus(ctx context.Context) (authStatus, error) {
	var v authStatus
	err := c.get(ctx, "/api/auth/status", nil, &v)
	return v, err
}

// ContextQuery is what `taskr context` reports about where the caller is.
//
// RemoteURL and Subpath are what let taskr resolve a project through the
// same locator a write resolves through — without Subpath a monorepo
// directory can never name its project here, only on a write, which is
// exactly the gap that let context go silent instead of reporting the
// ambiguity a write would refuse on. HeadSHA is what lets taskr record the
// observation rot detection is built on. None of these are collected by
// running git — that boundary is why taskr has no integration to break —
// so they are omitted whenever the caller does not already know them.
type ContextQuery struct {
	Machine string
	// AgentSessionID is what makes this invocation's context its own.
	// park and end resolve their session through here, so leaving it off a
	// machine running two agents files one agent's resume note against the
	// other's issue.
	AgentSessionID string
	CWD            string
	RemoteURL      string
	Subpath        string
	HeadSHA        string
}

// Context is the HTTP mirror of the taskr_context MCP tool.
func (c *Client) Context(ctx context.Context, in ContextQuery) (ContextView, error) {
	q := url.Values{}
	setIf(q, "machine", in.Machine)
	setIf(q, "agent_session_id", in.AgentSessionID)
	setIf(q, "cwd", in.CWD)
	setIf(q, "remote_url", in.RemoteURL)
	setIf(q, "subpath", in.Subpath)
	setIf(q, "head_sha", in.HeadSHA)
	var v ContextView
	err := c.get(ctx, "/api/context", q, &v)
	return v, err
}

// Next returns the ranked candidate list, scoped to the caller's project
// unless all is set. untriaged drops the actionable-verdict gate, which is
// how the caller sees work that has been filed but not yet triaged.
func (c *Client) Next(ctx context.Context, machine string, loc Locator, all, untriaged bool) ([]Candidate, error) {
	q := url.Values{}
	setIf(q, "machine", machine)
	setIf(q, "remote_url", loc.RemoteURL)
	setIf(q, "subpath", loc.Subpath)
	if all {
		q.Set("all", "1")
	}
	if untriaged {
		q.Set("include_untriaged", "1")
	}
	var v []Candidate
	err := c.get(ctx, "/api/next", q, &v)
	return v, err
}

// TriageQueue lists the open issues that need a fresh verdict — never
// triaged, verdict expired, or the repo moved under them — scoped to the
// caller's project unless all is set. ref narrows it to one issue: an empty
// answer then means that issue's verdict is fresh, and a ref naming no
// issue is an error rather than an empty queue.
func (c *Client) TriageQueue(ctx context.Context, loc Locator, ref string, all bool) ([]TriageCandidate, error) {
	q := url.Values{}
	setIf(q, "ref", ref)
	setIf(q, "remote_url", loc.RemoteURL)
	setIf(q, "subpath", loc.Subpath)
	if all {
		q.Set("all", "1")
	}
	var v []TriageCandidate
	err := c.get(ctx, "/api/triage", q, &v)
	return v, err
}

// ListIssues searches issues by free text and status, scoped to the
// caller's project unless all is set.
//
// It asks the server for the agent format and returns the envelope. An older
// server answers with a bare row array instead — the first byte tells those
// apart — and comes back as a version-0 envelope: rows intact, meta empty,
// exactly what a pre-agent-search backend could say.
func (c *Client) ListIssues(ctx context.Context, query string, status []string, loc Locator, all bool) (AgentSearchResponse, error) {
	q := url.Values{}
	q.Set("format", "agent")
	setIf(q, "q", query)
	for _, s := range status {
		q.Add("status", s)
	}
	setIf(q, "remote_url", loc.RemoteURL)
	setIf(q, "subpath", loc.Subpath)
	if all {
		q.Set("all", "1")
	}
	body, err := c.do(ctx, http.MethodGet, "/api/issues", q, nil)
	if err != nil {
		return AgentSearchResponse{}, err
	}
	if len(body) > 0 && body[0] == '[' {
		var rows []SearchResult
		if err := json.Unmarshal(body, &rows); err != nil {
			return AgentSearchResponse{}, err
		}
		return AgentSearchResponse{Results: rows}, nil
	}
	var v AgentSearchResponse
	err = json.Unmarshal(body, &v)
	return v, err
}

// GetIssue fetches one issue by id or human ref (e.g. "TSK-12").
func (c *Client) GetIssue(ctx context.Context, ref string, agentContext bool) (IssueView, error) {
	q := url.Values{}
	if agentContext {
		q.Set("agent_context", "1")
	}
	var v IssueView
	err := c.get(ctx, "/api/issues/"+url.PathEscape(ref), q, &v)
	return v, err
}

// ListDocuments returns the documents linked to one issue.
func (c *Client) ListDocuments(ctx context.Context, ref string) ([]DocumentRef, error) {
	var v []DocumentRef
	err := c.get(ctx, "/api/issues/"+url.PathEscape(ref)+"/documents", nil, &v)
	return v, err
}

// Timeline returns one issue's event ledger, in stream order.
func (c *Client) Timeline(ctx context.Context, ref string) ([]TimelineEntry, error) {
	var v []TimelineEntry
	err := c.get(ctx, "/api/issues/"+url.PathEscape(ref)+"/timeline", nil, &v)
	return v, err
}

// Catchup returns how an issue got from 0 to now, under a token budget.
//
// budget of 0 leaves the ceiling to the server, which has a default per
// layer and clamps anything absurd rather than refusing it — the CLI does
// not second-guess either, so a caller who typed a silly number still gets
// a packet.
func (c *Client) Catchup(ctx context.Context, ref string, budget int, deep bool) (CatchupPacket, error) {
	q := url.Values{}
	if budget > 0 {
		q.Set("budget", strconv.Itoa(budget))
	}
	if deep {
		q.Set("deep", "1")
	}
	var v CatchupPacket
	err := c.get(ctx, "/api/issues/"+url.PathEscape(ref)+"/catchup", q, &v)
	return v, err
}

// --- Writes ---

// GitSnapshotInput is the tree state a write records on an issue: where the
// work is, on what branch, at which commit. It is what makes `taskr start`
// print a real "Tree state:" block instead of "no git snapshot has been
// recorded for this issue" (TSK-110).
//
// Every field arrives as an environment variable, whether the caller
// exported it or envWithRepo read it out of .git for them — nothing here
// runs git. See gitSnapshot for which variable feeds which field. Branch is
// omitempty on purpose: a detached HEAD has no branch, and the head is
// still worth recording, so the field goes missing rather than empty and
// the renderer says so out loud.
type GitSnapshotInput struct {
	Repo       string   `json:"repo,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	HeadSHA    string   `json:"head_sha"`
	DirtyFiles []string `json:"dirty_files,omitempty"`
	Worktree   string   `json:"worktree,omitempty"`
	MergeBase  string   `json:"merge_base,omitempty"`
}

// CreateIssueInput is the wire shape POST /api/issues accepts.
//
// The server routes on: ProjectSlug when set; then AdHoc; then Locator;
// then it refuses. An explicit --project is more deliberate than wherever
// the caller happens to be standing, and --adhoc is more deliberate still
// than the standing, because a stray thought had while sitting in a
// checkout is not that checkout's work. Locator carries the rest: the repo
// and directory a caller never named a project for.
//
// AdHoc is an opt-in and never a fallback. A write that simply lost its
// locator still fails, because "this belongs to no project" and "you told
// me nothing" are different answers and only one is safe to act on.
type CreateIssueInput struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Priority    string            `json:"priority,omitempty"`
	ProjectSlug string            `json:"project,omitempty"`
	AdHoc       bool              `json:"adhoc,omitempty"`
	Locator     Locator           `json:"locator,omitempty"`
	Snapshot    *GitSnapshotInput `json:"git_snapshot,omitempty"`
}

func (c *Client) CreateIssue(ctx context.Context, in CreateIssueInput) (CreatedIssue, error) {
	var v CreatedIssue
	err := c.write(ctx, http.MethodPost, "/api/issues", in, &v)
	return v, err
}

// AddComment posts a comment on an issue.
// UpsertDocumentInput is the wire shape POST /api/documents accepts. It is
// spelled upsert rather than create because DocumentID revises an existing
// document instead of opening a second one — the same call attaches a spec
// and later replaces its body.
//
// There is deliberately no Actor field. Authorship comes from the key in
// TASKR_KEY and a body that claims otherwise is ignored (518c0ac), so
// carrying one here would only invite a caller to believe it worked.
type UpsertDocumentInput struct {
	DocumentID  string `json:"document_id,omitempty"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	DiffSummary string `json:"diff_summary,omitempty"`
	LinkIssue   string `json:"link_issue,omitempty"`
}

// DocumentView is one document with its body, for `taskr doc show`.
type DocumentView struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	Revisions    int      `json:"revisions"`
	SupersededBy string   `json:"superseded_by,omitempty"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	LinkedIssues []string `json:"linked_issues,omitempty"`
}

// DocumentRevision is one row of a document's append-only history, for
// `taskr doc history`. It carries metadata only — the body itself stays in
// the blob store behind its sha256, and a list that inlined every past
// body would be unreadable at exactly the moment it became useful.
type DocumentRevision struct {
	Revision    int    `json:"revision"`
	SHA256      string `json:"sha256"`
	DiffSummary string `json:"diff_summary,omitempty"`
	RevisedAt   string `json:"revised_at"`
}

func (c *Client) UpsertDocument(ctx context.Context, in UpsertDocumentInput) (DocumentRef, error) {
	var out DocumentRef
	err := c.write(ctx, http.MethodPost, "/api/documents", in, &out)
	return out, err
}

func (c *Client) GetDocument(ctx context.Context, id string) (DocumentView, error) {
	var v DocumentView
	err := c.get(ctx, "/api/documents/"+url.PathEscape(id), nil, &v)
	return v, err
}

func (c *Client) GetDocumentRevisions(ctx context.Context, id string) ([]DocumentRevision, error) {
	var rows []DocumentRevision
	err := c.get(ctx, "/api/documents/"+url.PathEscape(id)+"/revisions", nil, &rows)
	return rows, err
}

// UpdateIssueInput is the subset of PATCH /api/issues/{ref} the CLI sends.
// The endpoint takes more than this; a field left empty is untouched
// server-side, so `close` sends only what closing means.
// UpdateIssueInput is the subset of PATCH /api/issues/{ref} the CLI sends.
// Empty means untouched, matching the server's contract — except
// Description, a pointer so an explicit "" clears the brief while an
// omitted field leaves it alone. Kind is deliberately absent: the server
// treats it as a wire constant fixed at creation, so edit cannot offer it.
type UpdateIssueInput struct {
	Title         string  `json:"title,omitempty"`
	Description   *string `json:"description,omitempty"`
	Priority      string  `json:"priority,omitempty"`
	Status        string  `json:"status,omitempty"`
	Resolution    string  `json:"resolution,omitempty"`
	DespiteChecks bool    `json:"despite_checks,omitempty"`
	SessionID     string  `json:"session_id,omitempty"`
}

// UpdateIssue is the write behind `taskr close` and `taskr edit`. It is
// spelled generally rather than as CloseIssue because the endpoint is
// general: one PATCH carries both the close and the reprioritise.
//
// The result carries a GroupHint when the closed issue belongs to a group —
// the parent to check back on, and the sibling to pick up next — so the
// caller never has to fetch the issue a second time just to learn that.
func (c *Client) UpdateIssue(ctx context.Context, ref string, in UpdateIssueInput) (UpdateIssueResult, error) {
	var out UpdateIssueResult
	err := c.write(ctx, http.MethodPatch, "/api/issues/"+url.PathEscape(ref), in, &out)
	return out, err
}

// AddChild adds an existing issue to a group.
// RevokeKey revokes one of the caller org's keys by id. The server scopes
// the delete to the caller's org and refuses an id outside it, so a wrong
// id reads the same as a dead one: not found.
func (c *Client) RevokeKey(ctx context.Context, id string) error {
	_, err := c.doWrite(ctx, http.MethodDelete, "/api/keys/"+url.PathEscape(id), nil)
	return err
}

func (c *Client) AddChild(ctx context.Context, parent, child string) error {
	req := struct {
		Child string `json:"child"`
	}{Child: child}
	return c.write(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(parent)+"/children", req, nil)
}

// RemoveChild takes an issue out of a group. The issue itself lives on.
func (c *Client) RemoveChild(ctx context.Context, parent, child string) error {
	return c.write(ctx, http.MethodDelete,
		"/api/issues/"+url.PathEscape(parent)+"/children/"+url.PathEscape(child), struct{}{}, nil)
}

// RelateInput is the wire shape POST /api/issues/{ref}/relate accepts.
// Remove turns the same endpoint into a delete — taskr unrelate sends
// Remove: true rather than hitting a second path — so it carries no
// omitempty: false is relate's own meaningful value, not a zero one to
// hide.
type RelateInput struct {
	To        string `json:"to"`
	Type      string `json:"type"`
	Remove    bool   `json:"remove"`
	SessionID string `json:"session_id,omitempty"`
}

// RelateIssues writes or removes a directed relationship between two
// issues, answering 204. PARENT_OF and CHILD_OF are refused by the
// aggregate — group membership is `taskr group add`/`rm`'s job, not
// relate's — so a caller should reject those locally before this ever
// runs; see validateRelType.
func (c *Client) RelateIssues(ctx context.Context, ref string, in RelateInput) error {
	return c.write(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/relate", in, nil)
}

func (c *Client) AddComment(ctx context.Context, ref, body string) error {
	req := struct {
		Body string `json:"body"`
	}{Body: body}
	return c.write(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/comments", req, nil)
}

// StartWorkInput is the wire shape POST /api/work/start accepts.
//
// AgentSessionID is not optional the way it reads. Sessions are keyed by
// machine, so a start that names no agent session adopts whatever session
// is already live there — including a running MCP agent's, which then
// carries a FocusSwitched onto an issue it knows nothing about. Sending an
// id makes this invocation a unit of work in its own right.
type StartWorkInput struct {
	Issue          string `json:"issue"`
	Machine        string `json:"machine"`
	Agent          string `json:"agent,omitempty"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	CWD            string `json:"cwd,omitempty"`
}

func (c *Client) StartWork(ctx context.Context, in StartWorkInput) (ResumePacket, error) {
	var v ResumePacket
	err := c.write(ctx, http.MethodPost, "/api/work/start", in, &v)
	return v, err
}

// ParkWorkInput is the wire shape POST /api/work/park accepts.
//
// The snapshot matters most here of the three: a park is the handoff, and
// the branch the work was left on is the single most useful fact it can
// carry to whoever resumes.
type ParkWorkInput struct {
	SessionID  string            `json:"session_id"`
	Reason     string            `json:"reason,omitempty"`
	ResumeNote string            `json:"resume_note,omitempty"`
	Snapshot   *GitSnapshotInput `json:"git_snapshot,omitempty"`
}

func (c *Client) ParkWork(ctx context.Context, in ParkWorkInput) error {
	return c.write(ctx, http.MethodPost, "/api/work/park", in, nil)
}

// EndWorkInput is the wire shape POST /api/work/end accepts.
type EndWorkInput struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

func (c *Client) EndWork(ctx context.Context, in EndWorkInput) error {
	return c.write(ctx, http.MethodPost, "/api/work/end", in, nil)
}

// OffloadInput is the wire shape POST /api/offload accepts.
//
// The server picks the project in this order: ProjectSlug when set; then
// AdHoc; then Locator — the repo the caller is standing in — when taskr
// knows it; then the org's inbox. The session is not a rung at all: a
// finding offloaded from another repo belongs to that repo, and the session
// only records where the caller was when they found it (TSK-59). A repo
// that serves several projects is still refused rather than guessed;
// ProjectSlug is how the caller decides.
//
// Unlike CreateIssueInput, AdHoc is not what an offload NEEDS to reach the
// inbox — an unresolvable locator lands there anyway, because noticing
// something must not derail the thing you were doing. It is how a caller
// says so deliberately from a repo that would otherwise have resolved.
type OffloadInput struct {
	SessionID   string            `json:"session_id"`
	Title       string            `json:"title"`
	Brief       string            `json:"brief"`
	Kind        string            `json:"kind,omitempty"`
	Severity    string            `json:"severity,omitempty"`
	ProjectSlug string            `json:"project,omitempty"`
	AdHoc       bool              `json:"adhoc,omitempty"`
	Locator     Locator           `json:"locator,omitempty"`
	Snapshot    *GitSnapshotInput `json:"git_snapshot,omitempty"`
}

func (c *Client) Offload(ctx context.Context, in OffloadInput) (CreatedOffload, error) {
	var v CreatedOffload
	err := c.write(ctx, http.MethodPost, "/api/offload", in, &v)
	return v, err
}

// --- Checks (TSK-90) ---

// DefineCheck adds a done-when to an issue.
func (c *Client) DefineCheck(ctx context.Context, ref string, in DefineCheckInput) (CheckRef, error) {
	var out CheckRef
	err := c.write(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/checks", in, &out)
	return out, err
}

// ListChecks reads an issue's checks with their latest runs.
func (c *Client) ListChecks(ctx context.Context, ref string) ([]CheckView, error) {
	var out []CheckView
	err := c.get(ctx, "/api/issues/"+url.PathEscape(ref)+"/checks", nil, &out)
	return out, err
}

// RunCheck records one execution of a check.
func (c *Client) RunCheck(ctx context.Context, checkID string, in RunCheckInput) (RunView, error) {
	var out RunView
	err := c.write(ctx, http.MethodPost, "/api/checks/"+url.PathEscape(checkID)+"/runs", in, &out)
	return out, err
}

// PendingChecks lists checks that have not passed, scoped like Next.
// runner narrows to "agent" or "human"; empty means both.
func (c *Client) PendingChecks(ctx context.Context, runner string, loc Locator, all bool) ([]PendingCheck, error) {
	q := url.Values{}
	if runner != "" {
		q.Set("runner", runner)
	}
	if all {
		q.Set("all", "1")
	}
	setIf(q, "remote_url", loc.RemoteURL)
	setIf(q, "subpath", loc.Subpath)
	var out []PendingCheck
	err := c.get(ctx, "/api/checks/pending", q, &out)
	return out, err
}

// --- Steps (TSK-105) ---

// ListSteps reads an issue's ordered working plan.
func (c *Client) ListSteps(ctx context.Context, ref string) ([]StepView, error) {
	var out []StepView
	err := c.get(ctx, "/api/issues/"+url.PathEscape(ref)+"/steps", nil, &out)
	return out, err
}

// AddSteps records one or more steps on an issue's plan, in the order given.
func (c *Client) AddSteps(ctx context.Context, ref string, in AddStepsInput) ([]StepRef, error) {
	var out []StepRef
	err := c.write(ctx, http.MethodPost, "/api/issues/"+url.PathEscape(ref)+"/steps", in, &out)
	return out, err
}

// EditStep revises a step's title and/or body.
func (c *Client) EditStep(ctx context.Context, stepID string, in EditStepInput) error {
	return c.write(ctx, http.MethodPatch, "/api/steps/"+url.PathEscape(stepID), in, nil)
}

// MoveStep relocates a step within its plan. An empty After moves it to
// the front.
func (c *Client) MoveStep(ctx context.Context, stepID string, in MoveStepInput) error {
	return c.write(ctx, http.MethodPut, "/api/steps/"+url.PathEscape(stepID)+"/position", in, nil)
}

// StartStep marks a step in progress.
func (c *Client) StartStep(ctx context.Context, stepID string, in StepMarkInput) (StepStatusResult, error) {
	var out StepStatusResult
	err := c.write(ctx, http.MethodPost, "/api/steps/"+url.PathEscape(stepID)+"/start", in, &out)
	return out, err
}

// DoneStep marks a step done.
func (c *Client) DoneStep(ctx context.Context, stepID string, in StepMarkInput) (StepStatusResult, error) {
	var out StepStatusResult
	err := c.write(ctx, http.MethodPost, "/api/steps/"+url.PathEscape(stepID)+"/done", in, &out)
	return out, err
}

// DropStep takes a step off the plan and returns the plan as it stands, so
// a caller can re-render without a second request.
func (c *Client) DropStep(ctx context.Context, stepID string, in DropStepInput) ([]StepView, error) {
	var out []StepView
	err := c.write(ctx, http.MethodPost, "/api/steps/"+url.PathEscape(stepID)+"/drop", in, &out)
	return out, err
}

// PromoteStep turns a step into a child issue or a check.
func (c *Client) PromoteStep(ctx context.Context, stepID string, in PromoteStepInput) (PromoteResult, error) {
	var out PromoteResult
	err := c.write(ctx, http.MethodPost, "/api/steps/"+url.PathEscape(stepID)+"/promote", in, &out)
	return out, err
}

// SubmitTriageInput is the wire shape POST /api/triage/{ref} accepts.
type SubmitTriageInput struct {
	Verdict     string `json:"verdict"`
	Evidence    string `json:"evidence,omitempty"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

func (c *Client) SubmitTriage(ctx context.Context, ref string, in SubmitTriageInput) error {
	return c.write(ctx, http.MethodPost, "/api/triage/"+url.PathEscape(ref), in, nil)
}

// --- Projects ---

// SetupProjectInput is the wire shape POST /api/projects accepts. `project
// init` never sends Repos — attaching a repo or a directory is `project
// attach`'s job, kept as its own step so a caller onboarding a second
// directory never has to repeat the project's key.
//
// Conventions ride here rather than on AttachRepoInput because this is the
// only endpoint that takes them: POST /api/projects/{slug}/repos decodes
// remote_url, default_branch, local_path, machine and subpath, and nothing
// else. Sending them is safe on an existing project — SetupProject upserts
// on slug and refreshes a convention only when the incoming value is
// non-empty — so `project init` doubles as the way to set them later
// (TSK-111). Nil when the caller named none, so an init that says nothing
// about conventions cannot be read as saying anything.
type SetupProjectInput struct {
	Slug        string              `json:"slug"`
	Name        string              `json:"name,omitempty"`
	Key         string              `json:"key"`
	Conventions *ProjectConventions `json:"conventions,omitempty"`
}

// SetupProjectResult is what `project init` needs back: enough to confirm
// the project exists, plus the CLAUDE.md snippet an agent pastes into the
// repo it just onboarded.
type SetupProjectResult struct {
	ProjectID       string `json:"project_id"`
	Key             string `json:"key"`
	ClaudeMDSnippet string `json:"claude_md_snippet"`
}

func (c *Client) SetupProject(ctx context.Context, in SetupProjectInput) (SetupProjectResult, error) {
	var v SetupProjectResult
	err := c.write(ctx, http.MethodPost, "/api/projects", in, &v)
	return v, err
}

// ListProjects returns every registered project, repos and dirs included —
// `project ls` is what an agent reads to decide whether a project already
// covers where it is standing.
func (c *Client) ListProjects(ctx context.Context) ([]ProjectView, error) {
	var v []ProjectView
	err := c.get(ctx, "/api/projects", nil, &v)
	return v, err
}

// AttachRepoInput is the wire shape POST /api/projects/{slug}/repos accepts.
// It is distinct from the app-layer type of the same name — the CLI is an
// HTTP client and nothing else, and never imports internal/app.
type AttachRepoInput struct {
	RemoteURL     string `json:"remote_url"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Subpath       string `json:"subpath,omitempty"`
	Machine       string `json:"machine,omitempty"`
}

func (c *Client) AttachRepo(ctx context.Context, slug string, in AttachRepoInput) error {
	return c.write(ctx, http.MethodPost, "/api/projects/"+url.PathEscape(slug)+"/repos", in, nil)
}

// RenameProject changes a project's slug and, when name is non-empty, its
// display name too.
func (c *Client) RenameProject(ctx context.Context, slug, newSlug, name string) error {
	req := struct {
		Slug string `json:"slug"`
		Name string `json:"name,omitempty"`
	}{Slug: newSlug, Name: name}
	return c.write(ctx, http.MethodPost, "/api/projects/"+url.PathEscape(slug)+"/rename", req, nil)
}

// Login confirms key is accepted by the target host before authLogin writes
// it to disk. The CLI itself never needs a session cookie — every request
// already carries X-Taskr-Key — so this used to be a side effect of hitting
// POST /api/auth/login with the key in the body, purely for the live round
// trip that failed on a bad key.
//
// That endpoint no longer accepts a key at all: browser-auth deletes the
// key-for-cookie exchange rather than deprecating it, since a session must
// now come from a password login. GET /api/auth/status, presenting key as
// an X-Taskr-Key header, is the same live round trip without depending on
// the endpoint this phase removes — it 401s (Authenticated: false) on a bad
// key exactly as the old call errored on one.
func (c *Client) Login(ctx context.Context, key string) error {
	probe := &Client{BaseURL: c.BaseURL, Key: key, HTTP: c.HTTP}
	status, err := probe.AuthStatus(ctx)
	if err != nil {
		return err
	}
	if !status.Authenticated {
		return &APIError{Status: http.StatusUnauthorized, Message: "key was not accepted"}
	}
	return nil
}

func setIf(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// --- Device flow (RFC 8628) ---

// DeviceCodeView is what POST /api/auth/device/code answers — RFC 8628
// §3.2. DeviceCode is the credential half and is never printed.
type DeviceCodeView struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// MintedKeyView is the key the poll hands back, once.
type MintedKeyView struct {
	ID     string   `json:"id"`
	Key    string   `json:"key"`
	Name   string   `json:"name"`
	Actor  string   `json:"actor"`
	Scopes []string `json:"scopes"`
}

// DeviceCode opens a device authorization. It carries no credential — that
// is the point of the grant — so it uses the plain post path rather than
// write, which would attach an Idempotency-Key this endpoint does not want.
func (c *Client) DeviceCode(ctx context.Context, clientName string) (DeviceCodeView, error) {
	req, err := json.Marshal(map[string]string{"client_name": clientName})
	if err != nil {
		return DeviceCodeView{}, err
	}
	body, err := c.do(ctx, http.MethodPost, "/api/auth/device/code", nil, req)
	if err != nil {
		return DeviceCodeView{}, err
	}
	var v DeviceCodeView
	return v, json.Unmarshal(body, &v)
}

// DeviceToken is one poll. A non-2xx comes back as *APIError whose Message
// is the server's own text, which for this endpoint is one of RFC 8628's
// error codes verbatim — so a caller branches on err.Error().
func (c *Client) DeviceToken(ctx context.Context, deviceCode string) (MintedKeyView, error) {
	req, err := json.Marshal(map[string]string{"device_code": deviceCode})
	if err != nil {
		return MintedKeyView{}, err
	}
	body, err := c.do(ctx, http.MethodPost, "/api/auth/device/token", nil, req)
	if err != nil {
		return MintedKeyView{}, err
	}
	var v MintedKeyView
	return v, json.Unmarshal(body, &v)
}

// Neighbors fetches the semantic suggestions for one issue (TSK-167):
// open issues whose text resembles it, with no existing edge between them.
func (c *Client) Neighbors(ctx context.Context, ref string) ([]Neighbor, error) {
	var v []Neighbor
	err := c.get(ctx, "/api/issues/"+url.PathEscape(ref)+"/neighbors", nil, &v)
	return v, err
}
