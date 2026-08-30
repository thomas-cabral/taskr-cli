// cli/context_test.go
package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// mustDecode decodes body as T the way the client would, so the tests
// exercise the same unmarshal path a live server response takes.
func mustDecode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decoding test body: %v", err)
	}
	return v
}

// parkedRow is a wire-shaped row as the server's loadParkedTx sends it,
// with the fields an older binary dropped omitted the way omitempty does.
func parkedRow(id, ref, title, reason, note, parkedAt string, alsoParked int) string {
	b := `{"id":"` + id + `","machine":"lab","issue_id":"01a045bb","status":"parked","started_at":"2026-08-27T23:55:29Z"`
	if ref != "" {
		b += `,"issue_ref":"` + ref + `"`
	}
	if title != "" {
		b += `,"issue_title":"` + title + `"`
	}
	if reason != "" {
		b += `,"reason":"` + reason + `"`
	}
	if note != "" {
		b += `,"resume_note":"` + note + `"`
	}
	if parkedAt != "" {
		b += `,"parked_at":"` + parkedAt + `"`
	}
	if alsoParked > 0 {
		b += `,"also_parked":` + itoa(alsoParked)
	}
	return b + `}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// TestContextParkedRowsAreOneLiner is the budget TSK-220 puts orientation
// under: a parked row answers what it was, why it stopped and how long
// ago — on one line. The resume note is deliberately absent from the
// human render; it travels in --json and the resume packet instead.
func TestContextParkedRowsAreOneLiner(t *testing.T) {
	parkedAt := time.Now().Add(-3 * time.Hour).UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	v := ContextView{Parked: []SessionView{mustDecode[SessionView](t, parkedRow(
		"01a045a6", "TSK-216", "Site copy: replace 'context runs out' framing", "done_for_now",
		"Review and merge the PR, then close TSK-216.", parkedAt, 0,
	))}}
	out := RenderContext(v, "agent")
	for _, want := range []string{
		"Parked sessions (1, unfinished first):",
		"TSK-216 — Site copy: replace 'context runs out' framing",
		"parked 3h ago",
		"done for now",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\noutput:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"next:", "Review and merge the PR"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("resume note leaked into the parked row (%q):\n%s", unwanted, out)
		}
	}
	if strings.Contains(out, "01a045a6") {
		t.Errorf("session uuid leaked into a row that has a ref:\n%s", out)
	}
	if rows := strings.Count(out, "TSK-216"); rows != 1 {
		t.Errorf("row is not one line — ref appears %d times:\n%s", rows, out)
	}
}

// TestContextParkedRowShowsAlsoParked: the list is keyed by issue, so the
// sibling count is the only place an older park stays visible — even in
// the one-line budget.
func TestContextParkedRowShowsAlsoParked(t *testing.T) {
	parkedAt := time.Now().Add(-2 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	v := ContextView{Parked: []SessionView{mustDecode[SessionView](t, parkedRow(
		"01a0399d", "TSK-99", "Blocked on review", "blocked", "", parkedAt, 2,
	))}}
	out := RenderContext(v, "agent")
	for _, want := range []string{
		"TSK-99 — Blocked on review",
		"parked 2d ago",
		"blocked",
		"also parked: 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\noutput:\n%s", want, out)
		}
	}
}

// TestContextParkedRowFallsBackToIDs: an older server sends no park fields
// at all. The uuid row is what this fix exists to replace, but a renderer
// that crashes on it would be worse than a useless one.
func TestContextParkedRowFallsBackToIDs(t *testing.T) {
	v := ContextView{Parked: []SessionView{mustDecode[SessionView](t, parkedRow(
		"01a033e7", "", "", "", "", "", 0,
	))}}
	out := RenderContext(v, "agent")
	if !strings.Contains(out, "issue 01a045bb on lab") {
		t.Errorf("row without a ref should show the issue id on the machine:\n%s", out)
	}
}

// TestRelativeAge pins the buckets: minutes under an hour, hours under a
// day, days under a month, then the calendar date — and no crash on an
// empty or unparseable timestamp.
func TestRelativeAge(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ts   string
		want string
	}{
		{now.Format(time.RFC3339Nano), "just now"},
		{now.Add(-45 * time.Minute).Format(time.RFC3339Nano), "45m ago"},
		{now.Add(-3 * time.Hour).Format(time.RFC3339Nano), "3h ago"},
		{now.Add(-2 * 24 * time.Hour).Format(time.RFC3339Nano), "2d ago"},
		{now.Add(-40 * 24 * time.Hour).Format(time.RFC3339Nano), "Jul 19"},
		{"", ""},
		{"not a timestamp", ""},
	}
	for _, c := range cases {
		if got := relativeAge(now, c.ts); got != c.want {
			t.Errorf("relativeAge(now, %q) = %q, want %q", c.ts, got, c.want)
		}
	}
}

// TestParkReasonUnwrapsTheCodeTokens pins which reason codes read as code
// and get unwrapped; the rest pass through untouched.
func TestParkReasonUnwrapsTheCodeTokens(t *testing.T) {
	cases := map[string]string{
		"done_for_now":      "done for now",
		"context_exhausted": "context exhausted",
		"blocked":           "blocked",
		"handoff":           "handoff",
		"interrupted":       "interrupted",
	}
	for in, want := range cases {
		if got := parkReason(in); got != want {
			t.Errorf("parkReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestContextParkedBlockSaysWhatItIsNotShowing is the render half of
// TSK-246. The server caps this block and drops anything past its window, so
// a five-row list can be five parks or five of twenty — and a reader who
// cannot tell reads the second as the first and stops looking.
func TestContextParkedBlockSaysWhatItIsNotShowing(t *testing.T) {
	parkedAt := time.Now().Add(-5 * time.Hour).UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	v := ContextView{
		ParkedTotal: 20,
		Parked: []SessionView{mustDecode[SessionView](t, parkedRow(
			"01a045a6", "TSK-216", "Site copy", "blocked", "", parkedAt, 0,
		))},
	}
	out := RenderContext(v, "agent")
	for _, want := range []string{
		"Parked sessions (1 of 20, unfinished first):",
		"+19 older — taskr ls -s parked",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\noutput:\n%s", want, out)
		}
	}
}

// TestContextParkedBlockUntruncatedHasNoTail: a complete list must not carry
// a tail line inviting a reader to go looking for rows that do not exist.
func TestContextParkedBlockUntruncatedHasNoTail(t *testing.T) {
	parkedAt := time.Now().Add(-5 * time.Hour).UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	v := ContextView{
		ParkedTotal: 1,
		Parked: []SessionView{mustDecode[SessionView](t, parkedRow(
			"01a045a6", "TSK-216", "Site copy", "blocked", "", parkedAt, 0,
		))},
	}
	out := RenderContext(v, "agent")
	if !strings.Contains(out, "Parked sessions (1, unfinished first):") {
		t.Errorf("render did not use the complete-list heading:\n%s", out)
	}
	if strings.Contains(out, "older —") {
		t.Errorf("tail line rendered for a complete list:\n%s", out)
	}
}

// TestContextParkedTotalWithoutRowsStillSaysSo: when everything in scope is
// older than the server's window there are no rows, but an empty block would
// read as "nothing is parked" — the one thing that state is not.
func TestContextParkedTotalWithoutRowsStillSaysSo(t *testing.T) {
	out := RenderContext(ContextView{ParkedTotal: 12}, "agent")
	want := "Parked sessions: 12, none recent — taskr ls -s parked"
	if !strings.Contains(out, want) {
		t.Errorf("render missing %q\noutput:\n%s", want, out)
	}
}

// TestContextNoParksRendersNoBlock keeps the quiet case quiet: a machine with
// nothing parked pays nothing for the block.
func TestContextNoParksRendersNoBlock(t *testing.T) {
	out := RenderContext(ContextView{Machine: "lab"}, "agent")
	if strings.Contains(out, "Parked sessions") {
		t.Errorf("parked block rendered with nothing parked:\n%s", out)
	}
}

// TestContextParkedTotalFromOlderServerReadsAsComplete: a server predating
// ParkedTotal sends zero. Every row it does send is then the whole list, so
// the block must render as complete rather than claiming "1 of 0".
func TestContextParkedTotalFromOlderServerReadsAsComplete(t *testing.T) {
	parkedAt := time.Now().Add(-5 * time.Hour).UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	v := ContextView{Parked: []SessionView{mustDecode[SessionView](t, parkedRow(
		"01a045a6", "TSK-216", "Site copy", "blocked", "", parkedAt, 0,
	))}}
	out := RenderContext(v, "agent")
	if !strings.Contains(out, "Parked sessions (1, unfinished first):") {
		t.Errorf("render did not treat a zero total as a complete list:\n%s", out)
	}
	if strings.Contains(out, "older —") {
		t.Errorf("tail line rendered against a server that sends no total:\n%s", out)
	}
}
