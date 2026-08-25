// cli/device_test.go
package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
