# Check Verbs Implementation Plan (TSK-90)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `taskr check add|ls|run`, `close`'s 409 handling with `--despite-checks`, `show`'s checks table, and `next`'s needs-a-human block — the CLI for the checks API that shipped in taskr PR #7 (TSK-87) and is live on prod.

**Architecture:** Follow the repo's exact three-layer shape: typed `Client` methods over the HTTP API (`cli/client.go` + `cli/types.go`), one `cmd…`/`run…` function per verb in `cli/cli.go` using `flag.NewFlagSet` and `parseFlags`, renderers in `cli/render.go`. One structural addition: `APIError` gains the raw response `Body`, because the 409s this feature consumes (`pending_checks`, and later `last_entry_seq`) carry structure that `serverMessage` alone drops.

**Tech Stack:** Go (this repo's floor per go.mod), stdlib only, `httptest` wire tests (the `client_wire_test.go` pattern) and `Run(...)`-level command tests (the `helpers_test.go` pattern).

**Spec:** The API contract lives in the main repo: `docs/superpowers/specs/2026-08-23-topics-and-checks-design.md` (checks half) at `thomas-cabral/taskr`; this plan restates every wire shape it uses, so the spec is reference, not required reading. The server is live: routes `POST/GET /api/issues/{ref}/checks`, `POST /api/checks/{id}/runs`, `GET /api/checks/pending`, and `PATCH /api/issues/{ref}` with `despite_checks` answering 409 `{"error", "pending_checks":[{"id","title","runner"}]}` on a close over pending checks.

## Global Constraints

- Repo: `/home/cabralt/projects/taskr-cli`, branch `master` — create and work on branch `feat/check-verbs` (plain repo, not a worktree; `git checkout -b feat/check-verbs` first).
- Every command keeps `--json` parity (`printJSON(stdout, …)`).
- Writes go through `Client.write`/`doWrite` so the `Idempotency-Key` header is set; reads through `Client.get`.
- Usage text (`const usage`, cli.go:16) gains the new verbs in the style of the existing lines.
- `gofmt -l .` clean; `go test ./... -count=1` green; `go build ./...` clean at every commit.
- Conventional Commits; no model/vendor names; no Co-Authored-By trailer.

---

### Task 1: Client — types, APIError.Body, four methods

**Files:**
- Modify: `cli/client.go` — `APIError`, `doRaw`, four methods appended after `Offload`
- Modify: `cli/types.go` — check types appended
- Modify: `cli/client_wire_test.go` — wire tests appended

**Interfaces:**
- Produces: `APIError.Body []byte`; types `CheckRef{ID string; Issue IssueRef}`, `CheckView{ID, Title, Procedure, Expect, Runner, Status, CreatedAt string; LatestRun *RunView}`, `RunView{ID, Outcome string; Measurements []Measurement; EvidenceDocID, HeadSHA, Image, Note, Actor, RecordedAt string}`, `Measurement{Metric string; Value float64; Unit, Conditions string}` (json tags `metric/value/unit/conditions`, omitempty on unit+conditions), `PendingCheck{IssueID, IssueRef, IssueTitle, CheckID, Title, Runner string}` (json tags matching those names in snake_case), `PendingChecksBody{Error string; PendingChecks []PendingCheckItem}` with `PendingCheckItem{ID, Title, Runner string}`; `UpdateIssueInput` gains `DespiteChecks bool` json `despite_checks,omitempty`; methods `DefineCheck(ctx, ref string, in DefineCheckInput) (CheckRef, error)` with `DefineCheckInput{Title, Procedure, Expect, Runner string}`, `ListChecks(ctx, ref string) ([]CheckView, error)`, `RunCheck(ctx, checkID string, in RunCheckInput) (RunView, error)` with `RunCheckInput{Outcome string; Measurements []Measurement; EvidenceDocID, HeadSHA, Image, Note string}`, `PendingChecks(ctx, runner string, loc Locator, all bool) ([]PendingCheck, error)`.

- [ ] **Step 1: Write the failing wire tests**

Append to `cli/client_wire_test.go` (match the file's existing test style — read one existing test first and mirror its server/assert shape):

```go
func TestDefineCheckPostsAndDecodes(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"c-1","issue":{"id":"i-1","ref":"TSK-9"}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	ref, err := c.DefineCheck(context.Background(), "TSK-9", DefineCheckInput{
		Title: "lists fast", Procedure: "hey -z 30s", Expect: "> 100 r/s", Runner: "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /api/issues/TSK-9/checks" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"runner":"human"`) || !strings.Contains(gotBody, `"procedure":"hey -z 30s"`) {
		t.Fatalf("body = %s", gotBody)
	}
	if ref.ID != "c-1" || ref.Issue.Ref != "TSK-9" {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestRunCheckPostsMeasurements(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"42","outcome":"pass","recorded_at":"2026-08-23T00:00:00.000000000Z"}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	run, err := c.RunCheck(context.Background(), "c-1", RunCheckInput{
		Outcome: "pass",
		Measurements: []Measurement{{Metric: "list.rps", Value: 462, Unit: "r/s", Conditions: "c50"}},
		HeadSHA: "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /api/checks/c-1/runs" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"metric":"list.rps"`) || !strings.Contains(gotBody, `"value":462`) {
		t.Fatalf("body = %s", gotBody)
	}
	if run.ID != "42" || run.Outcome != "pass" {
		t.Fatalf("run = %+v", run)
	}
}

func TestListChecksAndPendingChecksGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/issues/TSK-9/checks":
			w.Write([]byte(`[{"id":"c-1","title":"t","runner":"agent","status":"pending","created_at":"x"}]`))
		case "/api/checks/pending":
			if r.URL.Query().Get("runner") != "human" || r.URL.Query().Get("all") != "1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte(`[{"issue_id":"i","issue_ref":"TSK-9","issue_title":"T","check_id":"c-1","title":"t","runner":"human"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	checks, err := c.ListChecks(context.Background(), "TSK-9")
	if err != nil || len(checks) != 1 || checks[0].Status != "pending" {
		t.Fatalf("checks = %+v, %v", checks, err)
	}
	pending, err := c.PendingChecks(context.Background(), "human", Locator{}, true)
	if err != nil || len(pending) != 1 || pending[0].IssueRef != "TSK-9" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
}

func TestAPIErrorCarriesTheRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"pending","pending_checks":[{"id":"c-1","title":"t","runner":"human"}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	_, err := c.UpdateIssue(context.Background(), "TSK-9", UpdateIssueInput{Status: "closed"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusConflict {
		t.Fatalf("status = %d", apiErr.Status)
	}
	var body PendingChecksBody
	if jsonErr := json.Unmarshal(apiErr.Body, &body); jsonErr != nil {
		t.Fatalf("Body not preserved: %v (%s)", jsonErr, apiErr.Body)
	}
	if len(body.PendingChecks) != 1 || body.PendingChecks[0].Runner != "human" {
		t.Fatalf("decoded = %+v", body)
	}
}
```

Add `"errors"`, `"encoding/json"`, `"io"`, `"strings"` to the test file's imports if absent.

- [ ] **Step 2: Run to verify red**

Run: `go test ./cli/ -run 'DefineCheck|RunCheckPosts|ListChecksAnd|APIErrorCarries' -count=1`
Expected: FAIL — undefined methods/types

- [ ] **Step 3: Implement**

`cli/client.go`: extend `APIError` and `doRaw`:

```go
type APIError struct {
	Status  int
	Message string
	// Body is the raw response body. Structured refusals — a close's 409
	// with pending_checks — carry more than one line, and serverMessage
	// keeps only the line; callers that can act on the structure decode
	// this instead.
	Body []byte
}
```

and in `doRaw`'s error branch:

```go
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Status: resp.StatusCode, Message: serverMessage(resp.StatusCode, respBody), Body: respBody}
	}
```

Append after `Offload`:

```go
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
	loc.fill(q)
	var out []PendingCheck
	err := c.get(ctx, "/api/checks/pending", q, &out)
	return out, err
}
```

Check how `Next` passes its locator (`grep -n "loc" cli/client.go` around `Next`) — if the locator contributes query params via a method with a different name than `fill`, use that exact mechanism; if `Next` inlines `remote_url`/`subpath` sets, do the same here.

`cli/types.go`, appended:

```go
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
```

Also add `DespiteChecks bool \`json:"despite_checks,omitempty"\`` to the existing `UpdateIssueInput` in types.go, and add `Checks []CheckView \`json:"checks,omitempty"\`` to the existing `IssueView` (after `Parent` or the last field).

- [ ] **Step 4: Run to verify green**

Run: `go test ./cli/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cli/client.go cli/types.go cli/client_wire_test.go
git commit -m "feat(client): check methods, structured error bodies"
```

---

### Task 2: `taskr check add|ls|run`

**Files:**
- Modify: `cli/cli.go` — `runCheck` dispatcher + three subcommands, `case "check":` in `Run`, usage text
- Modify: `cli/render.go` — `RenderChecks`
- Modify: `cli/helpers_test.go` or a new `cli/check_cmd_test.go` — command tests

**Interfaces:**
- Consumes: Task 1's client methods and types; `parseFlags`, `printJSON`, `stringList` (all existing in cli.go).
- Produces: `runCheck(ctx, c, rest, stdout, stderr) error`; `--measure metric=value[unit]` repeatable flag parsed by `parseMeasure(s, conditions string) (Measurement, error)`; `RenderChecks(w io.Writer, checks []CheckView)`.

- [ ] **Step 1: Write the failing command tests**

Create `cli/check_cmd_test.go` (mirror the harness the existing command tests use — read `helpers_test.go` first; the shape below assumes a `Run`-level call against an `httptest` server with env pinned, which is what `auth_status_test.go` does; adapt to the repo's actual helper names):

```go
package cli

import (
	"strings"
	"testing"
)

func TestParseMeasure(t *testing.T) {
	m, err := parseMeasure("list.p50=0.057s", "c50")
	if err != nil {
		t.Fatal(err)
	}
	if m.Metric != "list.p50" || m.Value != 0.057 || m.Unit != "s" || m.Conditions != "c50" {
		t.Fatalf("m = %+v", m)
	}
	m, err = parseMeasure("count=42", "")
	if err != nil || m.Unit != "" || m.Value != 42 {
		t.Fatalf("unitless: %+v, %v", m, err)
	}
	if _, err := parseMeasure("nometric", ""); err == nil {
		t.Fatal("missing = accepted")
	}
	if _, err := parseMeasure("m=notanumber", ""); err == nil {
		t.Fatal("non-numeric accepted")
	}
	if _, err := parseMeasure("m=12xyz3", ""); err == nil {
		t.Fatal("garbage suffix mid-number accepted")
	}
}
```

plus one end-to-end test per subcommand through the repo's command-test harness: `check add TSK-9 -t "lists fast" -m "hey -z 30s" --expect "> 100 r/s" --human` posts runner=human and prints the check id; `check ls TSK-9` renders a table containing title/runner/status; `check run c-1 --pass --measure list.rps=462r/s --conditions c50 --sha abc` posts outcome=pass with the measurement and prints the outcome. Write them in the harness's own idiom — the assertions above are the contract.

- [ ] **Step 2: Run to verify red**

Run: `go test ./cli/ -run 'ParseMeasure|CheckAdd|CheckLs|CheckRun' -count=1`
Expected: FAIL

- [ ] **Step 3: Implement**

In `cli/cli.go`, add to `Run`'s switch after `case "group":`:

```go
	case "check":
		run = func() error { return runCheck(ctx, client, rest, stdout, stderr, getenv) }
```

and the verb functions (place near cmdTriage):

```go
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
```

(`stringList` exists — cmdLs uses it for `-s`. Confirm `strconv` is imported.)

`cli/render.go`:

```go
// RenderChecks prints an issue's checks: title, runner, status, latest run.
func RenderChecks(w io.Writer, checks []CheckView) {
	if len(checks) == 0 {
		fmt.Fprintln(w, "No checks.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
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
```

(match render.go's actual table idiom — if it does not use `tabwriter`, mirror whatever `RenderIssueTable` does.)

Usage text: add under the issue verbs:

```
  check add <ref> -m <procedure> [--expect <text>] [--human]   record a done-when on an issue
  check ls <ref>                                               list an issue's checks
  check run <id> --pass|--fail [--measure metric=value[unit]]  record a result
```

- [ ] **Step 4: Run to verify green**

Run: `go test ./cli/ -count=1` and `go build ./...` and `gofmt -l .`
Expected: PASS / clean / empty

- [ ] **Step 5: Commit**

```bash
git add cli/cli.go cli/render.go cli/check_cmd_test.go
git commit -m "feat(cli): taskr check add, ls, run"
```

---

### Task 3: close's 409 + --despite-checks, show's checks table, next's needs-a-human block

**Files:**
- Modify: `cli/cli.go` — `cmdClose`, `cmdShow`, `cmdNext`
- Modify: `cli/check_cmd_test.go` — tests

**Interfaces:**
- Consumes: Tasks 1-2.
- Produces: `close` prints the pending list and the exact `--despite-checks` spelling on a 409; `close --despite-checks` sends `despite_checks:true`; `show` renders the checks table when the view carries checks; `next` (non-JSON) fetches `PendingChecks(ctx, "human", loc, *all)` after the queue and prints a "Needs a human:" block when non-empty (errors swallowed — the queue already printed, same courtesy-line rule as `liveSessionOn`).

- [ ] **Step 1: Write the failing tests**

In the command-test harness: (a) `close TSK-9` against a server answering 409 with the pending body → output contains the check title and the string `--despite-checks`, exit code non-zero; (b) `close TSK-9 --despite-checks` → request body contains `"despite_checks":true`; (c) `show TSK-9` where the issue view carries one check → output contains the check title and status; (d) `next` where `/api/next` answers one candidate and `/api/checks/pending` answers one human check → output contains "Needs a human" and the issue ref; (e) `next` where the pending call fails → queue still prints, no error. Contract assertions; write them in the harness idiom.

- [ ] **Step 2: Run to verify red**

- [ ] **Step 3: Implement**

`cmdClose`: add the flag and the 409 handling:

```go
	despite := fs.Bool("despite-checks", false, "close even though checks are pending; each is recorded as skipped")
```

pass it: `UpdateIssueInput{Status: "closed", Resolution: *resolution, DespiteChecks: *despite}`, and wrap the error return:

```go
	out, err := c.UpdateIssue(ctx, ref, UpdateIssueInput{Status: "closed", Resolution: *resolution, DespiteChecks: *despite})
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
			var body PendingChecksBody
			if json.Unmarshal(apiErr.Body, &body) == nil && len(body.PendingChecks) > 0 {
				fmt.Fprintf(stderr, "%s has pending checks:\n", ref)
				for _, p := range body.PendingChecks {
					fmt.Fprintf(stderr, "  %s (%s) — run it with `taskr check run %s --pass|--fail`\n", p.Title, p.Runner, p.ID)
				}
				fmt.Fprintf(stderr, "Run them, or close anyway with `taskr close %s --despite-checks`.\n", ref)
				return fmt.Errorf("close refused: %d checks pending", len(body.PendingChecks))
			}
		}
		return err
	}
```

`cmdShow` (non-JSON path), after the existing sections render:

```go
	if len(view.Checks) > 0 {
		fmt.Fprintln(stdout, "\nChecks:")
		RenderChecks(stdout, view.Checks)
	}
```

`cmdNext` (non-JSON path), after `RenderCandidates`:

```go
	// Pending human-run checks are work only a person can move; the agent
	// queue above never ranks them. Errors are swallowed: this is a
	// courtesy block after the real answer, same rule as liveSessionOn.
	if human, err := c.PendingChecks(ctx, "human", loc, *all); err == nil && len(human) > 0 {
		fmt.Fprintln(stdout, "\nNeeds a human:")
		for _, p := range human {
			fmt.Fprintf(stdout, "  %s — %s (`taskr check run %s --pass|--fail`)\n", p.IssueRef, p.Title, p.CheckID)
		}
	}
```

Add `"encoding/json"`, `"errors"`, `"net/http"` to cli.go's imports if absent.

- [ ] **Step 4: Run to verify green**

Run: `go test ./... -count=1`; `go build ./...`; `gofmt -l .`
Expected: green / clean / empty

- [ ] **Step 5: Commit**

```bash
git add cli/cli.go cli/check_cmd_test.go
git commit -m "feat(cli): close gates on pending checks; show and next surface them"
```

---

### Task 4: README, usage polish, full suite

**Files:**
- Modify: `README.md` — a short "Checks" section under the command docs, mirroring the existing sections' style: what a check is (a done-when that gates `taskr close`), the three verbs with one-line examples, `--despite-checks`.

- [ ] **Step 1: Write the README section** (style-match the file; content: the four commands above with the smoke-tested example `taskr check add TSK-9 -m "hey -z 30s -c 50 GET /api/issues" --expect "> 100 r/s" --human`).

- [ ] **Step 2: Full suite**

Run: `go build ./...`; `go test ./... -count=1`; `gofmt -l .`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: checks section"
```

The controller (not the implementer) then: pushes the branch, opens the PR against master, updates the local taskr skill text (`~/.claude/skills/taskr/SKILL.md` — outside this repo), and comments on TSK-90.

---

## Self-review

**Coverage vs TSK-90's brief:** check add/ls/run → Tasks 1-2; close 409 + `--despite-checks` → Task 3; show's checks table → Task 3; next's needs-a-human block via `GET /api/checks/pending?runner=human` with next's scoping → Tasks 1+3; README → Task 4; skill-text update explicitly reserved for the controller (machine-local file, not this repo).

**Placeholders:** the command-test steps in Tasks 2-3 state contracts and defer idiom to the repo's existing harness — deliberate, flagged in the step text, because the harness's helper names were not fully recon'd; the implementer reads `helpers_test.go` first. Everything else is concrete code.

**Type consistency:** `Measurement`/`CheckView`/`RunView`/`PendingCheck` json tags mirror the server's (verified against taskr's `internal/app/check.go`); `PendingChecksBody` matches the live 409 shape (smoke-verified on prod 2026-08-23); `UpdateIssueInput.DespiteChecks` json `despite_checks` matches the server's PATCH body field.
