// cli/device_test.go
package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// A pipe is what `echo $KEY | taskr auth login` gives us, and it must keep
// reading the key exactly as it always has.
func TestIsTerminalIsFalseForAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Skipf("os.Pipe unavailable: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("a pipe reported as a terminal; piped logins would start a device flow")
	}
}

// Every existing authLogin test passes a strings.Reader. They must keep
// exercising the stdin path, which is what makes this change safe.
func TestIsTerminalIsFalseForANonFile(t *testing.T) {
	if isTerminal(strings.NewReader("key")) {
		t.Error("a strings.Reader reported as a terminal")
	}
}

// The case that started this: a character device blocks io.ReadAll until
// Ctrl-D, which reads as a hang. /dev/ptmx is a character device on both
// Linux and macOS.
func TestIsTerminalIsTrueForACharacterDevice(t *testing.T) {
	f, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx on this platform: %v", err)
	}
	defer f.Close()
	if !isTerminal(f) {
		t.Error("a character device did not report as a terminal")
	}
}

func TestDeviceCodeDecodesTheRFCFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/device/code" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_code":"dc","user_code":"WDJB-MJHT",
			"verification_uri":"https://x/activate",
			"verification_uri_complete":"https://x/activate?code=WDJB-MJHT",
			"expires_in":600,"interval":5}`)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	got, err := c.DeviceCode(context.Background(), "taskr CLI on test-box")
	if err != nil {
		t.Fatalf("DeviceCode: %v", err)
	}
	if got.UserCode != "WDJB-MJHT" || got.Interval != 5 || got.ExpiresIn != 600 {
		t.Fatalf("decoded %+v", got)
	}
}

// The four RFC error codes have to survive the trip as themselves — the
// poll loop branches on them.
func TestDeviceTokenSurfacesTheRFCErrorCodes(t *testing.T) {
	for _, code := range []string{"authorization_pending", "slow_down", "access_denied", "expired_token"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":%q}`, code)
		}))
		c := &Client{BaseURL: srv.URL}
		_, err := c.DeviceToken(context.Background(), "dc")
		if err == nil || !strings.Contains(err.Error(), code) {
			t.Errorf("err = %v, want it to carry %q", err, code)
		}
		srv.Close()
	}
}

// The URL and the code are what a person acts on. The device code is the
// credential and must never reach the screen.
func TestDeviceLoginPrintsTheURLAndCodeButNeverTheDeviceCode(t *testing.T) {
	// A successful poll calls saveKey, which writes
	// $XDG_CONFIG_HOME/taskr/hosts.json. Without this the test rewrites the
	// real config of whoever runs the suite — and hosts.json holding a
	// second host is exactly the failure TSK-60 was filed for.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/device/code":
			fmt.Fprint(w, `{"device_code":"SECRET-DEVICE-CODE","user_code":"WDJB-MJHT",
				"verification_uri":"https://x/activate",
				"verification_uri_complete":"https://x/activate?code=WDJB-MJHT",
				"expires_in":600,"interval":0}`)
		case "/api/auth/device/token":
			polls++
			if polls < 2 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"authorization_pending"}`)
				return
			}
			fmt.Fprint(w, `{"id":"k-1","key":"tk_live","name":"taskr CLI","actor":"agent"}`)
		default:
			fmt.Fprint(w, `{"authenticated":true,"required":true,"actor":"agent"}`)
		}
	}))
	defer srv.Close()

	var out, errb bytes.Buffer
	err := authLoginDevice(context.Background(), srv.URL, "taskr CLI on test-box", &out, &errb)
	if err != nil {
		t.Fatalf("authLoginDevice: %v", err)
	}
	got := out.String()
	for _, want := range []string{"https://x/activate", "WDJB-MJHT"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "SECRET-DEVICE-CODE") {
		t.Error("the device code was printed; it is the credential and must stay off screen")
	}
	if polls != 2 {
		t.Errorf("polled %d times, want 2 (one pending, one success)", polls)
	}

	// The one thing this whole task exists to do: the minted key actually
	// lands in hosts.json, under the right host.
	hosts, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	host := hostKey(srv.URL)
	if hosts.Hosts[host].Key != "tk_live" {
		t.Errorf("stored key for %s = %q, want tk_live", host, hosts.Hosts[host].Key)
	}
}

// slow_down means the server is asking for less traffic, and a client that
// ignores it is the reason the code exists.
func TestSlowDownIncreasesTheInterval(t *testing.T) {
	var intervals []time.Duration
	sleep := func(d time.Duration) { intervals = append(intervals, d) }

	// saveKey writes $XDG_CONFIG_HOME/taskr/hosts.json, so every test that
	// reaches a successful poll MUST redirect it or it rewrites the real
	// config of whoever runs the suite.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Pending first, then slow_down, then success — two sleeps, which is
	// what makes "the second is longer" a statement about slow_down rather
	// than about there being only one sleep.
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		polls++
		switch polls {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"slow_down"}`)
		default:
			fmt.Fprint(w, `{"id":"k-1","key":"tk_live","actor":"agent"}`)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := pollDevice(context.Background(), &Client{BaseURL: srv.URL}, "dc", 5, 600, sleep, &out); err != nil {
		t.Fatalf("pollDevice: %v", err)
	}
	if len(intervals) != 2 {
		t.Fatalf("slept %d times (%v), want 2", len(intervals), intervals)
	}
	if intervals[0] != 5*time.Second || intervals[1] != 10*time.Second {
		t.Fatalf("intervals = %v, want [5s 10s] — slow_down adds five", intervals)
	}
}

func TestDeniedApprovalStopsWithAClearMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"access_denied"}`)
	}))
	defer srv.Close()

	var out bytes.Buffer
	err := pollDevice(context.Background(), &Client{BaseURL: srv.URL}, "dc", 0, 600,
		func(time.Duration) {}, &out)
	// "nothing was granted" is pollDevice's own wording for the
	// access_denied case, not the server's raw error code — asserting on it
	// pins the mapping. Asserting on "denied" alone would also pass if the
	// access_denied case were deleted from the switch, since the raw
	// "access_denied" that falls through to default still contains it.
	if err == nil || !strings.Contains(err.Error(), "nothing was granted") {
		t.Fatalf("err = %v, want it to say nothing was granted", err)
	}
}
