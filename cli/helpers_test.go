// cli/helpers_test.go
package cli_test

import (
	"fmt"
	"net/http"
)

// recordingHandler answers every request with a well-formed auth/status
// body, after copying the X-Taskr-Key header it received into *got — enough
// for a test that only cares what the client sent, not what a real server
// would do with it.
func recordingHandler(got *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*got = r.Header.Get("X-Taskr-Key")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"authenticated":false,"required":false}`)
	}
}

// idempotencyRecordingHandler records each request's Idempotency-Key header,
// in order, and answers with a well-formed IssueRef body.
func idempotencyRecordingHandler(seen *[]string) http.HandlerFunc {
	n := 0
	return func(w http.ResponseWriter, r *http.Request) {
		n++
		*seen = append(*seen, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"id-%d","ref":"TSK-%d"}`, n, n)
	}
}
