// cli/close_steps_test.go
package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCloseReportsAbandonedSteps: the server has always answered a close
// with abandoned_steps, and this client decoded the response without the
// field, so the list went in the bin. Steps never gate a close — this line
// is the only moment anyone learns what the plan did not reach (TSK-113).
func TestCloseReportsAbandonedSteps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/issues/TSK-1":
			fmt.Fprint(w, `{"id":"i-1","ref":"TSK-1","abandoned_steps":[
				{"id":"s-2","position":2,"title":"add the cookie fallback","status":"abandoned"},
				{"id":"s-3","position":3,"title":"cover the empty-header case","status":"abandoned"}]}`)
		case "/api/context":
			fmt.Fprint(w, `{"machine":"laptop"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"close", "TSK-1", "-r", "shipped"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, want := range []string{"add the cookie fallback", "cover the empty-header case", "offload"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("close output does not mention %q:\n%s", want, out.String())
		}
	}
}

// TestCloseSaysNothingAboutStepsWhenNoneWereAbandoned keeps the line honest:
// a close that finished its plan (or never had one) must not print an
// empty heading.
func TestCloseSaysNothingAboutStepsWhenNoneWereAbandoned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/issues/TSK-1":
			fmt.Fprint(w, `{"id":"i-1","ref":"TSK-1"}`)
		case "/api/context":
			fmt.Fprint(w, `{"machine":"laptop"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	env := envAt(map[string]string{"TASKR_API": srv.URL, "TASKR_KEY": "x"})
	if code := Run([]string{"close", "TSK-1"}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "unfinished") {
		t.Errorf("close printed an abandoned-step heading with no steps:\n%s", out.String())
	}
}
