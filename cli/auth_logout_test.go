// cli/auth_logout_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// logoutServer is a fake host that answers the two calls logout makes: it
// records whether GET /api/auth/status and DELETE /api/keys/{id} arrived,
// and can be told to fail either one.
type logoutServer struct {
	srv        *httptest.Server
	statusHits int
	deleteHits int
	keyID      string
	failStatus bool
	failDelete bool
}

func newLogoutServer(t *testing.T, keyID string) *logoutServer {
	t.Helper()
	l := &logoutServer{keyID: keyID}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		l.statusHits++
		if l.failStatus {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, "upstream gone")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true,
			"actor":         "agent",
			"key_id":        l.keyID,
		})
	})
	mux.HandleFunc("DELETE /api/keys/", func(w http.ResponseWriter, r *http.Request) {
		l.deleteHits++
		if l.failDelete {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "nope")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	l.srv = httptest.NewServer(mux)
	t.Cleanup(l.srv.Close)
	return l
}

func runLogout(getenv func(string) string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	err := authLogoutCmd(nil, &stdout, &stderr, getenv)
	return stdout.String(), stderr.String(), err
}

func TestLogoutRevokesAndForgets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	l := newLogoutServer(t, "k-123")
	if err := saveKey(hostKey(normalizeBaseURL(l.srv.URL)), "tk_live"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}
	getenv := envFrom(map[string]string{"TASKR_API": l.srv.URL})

	stdout, _, err := runLogout(getenv)
	if err != nil {
		t.Fatalf("authLogoutCmd: %v", err)
	}
	if l.statusHits != 1 || l.deleteHits != 1 {
		t.Errorf("status hits = %d, delete hits = %d, want 1 and 1", l.statusHits, l.deleteHits)
	}
	hosts, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts after logout: %v", err)
	}
	host := hostKey(normalizeBaseURL(l.srv.URL))
	if _, ok := hosts.Hosts[host]; ok {
		t.Errorf("key for %s still stored after logout", host)
	}
	if hosts.Current == host {
		t.Errorf("current still points at the logged-out host")
	}
	if !strings.Contains(stdout, "Revoked key k-123") || !strings.Contains(stdout, "Forgotten") {
		t.Errorf("stdout missing revocation or forget confirmation:\n%s", stdout)
	}
}

// TestLogoutWithNetworkDownStillForgets pins the design point: a logout that
// cannot reach the host must still take the key off the machine, and must
// say on stderr that the server-side credential is still live.
func TestLogoutWithNetworkDownStillForgets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// A saved key against a host that is not listening.
	dead := "http://127.0.0.1:1"
	if err := saveKey(hostKey(normalizeBaseURL(dead)), "tk_live"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}
	getenv := envFrom(map[string]string{"TASKR_API": dead})

	stdout, stderr, err := runLogout(getenv)
	if err != nil {
		t.Fatalf("authLogoutCmd with unreachable host: %v", err)
	}
	hosts, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts after logout: %v", err)
	}
	host := hostKey(normalizeBaseURL(dead))
	if _, ok := hosts.Hosts[host]; ok {
		t.Errorf("key for %s still stored after a failed-revoke logout", host)
	}
	if !strings.Contains(stderr, "still live server-side") {
		t.Errorf("stderr missing live-key warning:\n%s", stderr)
	}
	if strings.Contains(stdout, "Revoked") {
		t.Errorf("stdout claimed a revocation that did not happen:\n%s", stdout)
	}
}

// TestLogoutWithEnvKeyLeavesTheFileAlone pins the env edge: TASKR_KEY means
// there may be nothing stored to delete, and the file other hosts rely on
// must not be touched — but revocation is still attempted.
func TestLogoutWithEnvKeyLeavesTheFileAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	l := newLogoutServer(t, "k-env")
	if err := saveKey("other.example.com", "tk_other"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}
	getenv := envFrom(map[string]string{"TASKR_API": l.srv.URL, "TASKR_KEY": "tk_env"})

	stdout, _, err := runLogout(getenv)
	if err != nil {
		t.Fatalf("authLogoutCmd: %v", err)
	}
	if l.deleteHits != 1 {
		t.Errorf("delete hits = %d, want 1: env keys revoke too", l.deleteHits)
	}
	hosts, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if _, ok := hosts.Hosts["other.example.com"]; !ok {
		t.Errorf("logout deleted an unrelated stored host entry")
	}
	if !strings.Contains(stdout, "TASKR_KEY is set") {
		t.Errorf("stdout missing env-key note:\n%s", stdout)
	}
}

func TestLogoutWhenNotLoggedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	l := newLogoutServer(t, "k-unused")

	stdout, _, err := runLogout(envFrom(map[string]string{"TASKR_API": l.srv.URL}))
	if err != nil {
		t.Fatalf("authLogoutCmd: %v", err)
	}
	if l.statusHits != 0 || l.deleteHits != 0 {
		t.Errorf("server hit %d/%d times with no credential stored, want 0/0", l.statusHits, l.deleteHits)
	}
	if !strings.Contains(stdout, "Not logged in") {
		t.Errorf("stdout missing not-logged-in message:\n%s", stdout)
	}
}

// TestLogoutAlreadyRevokedKey pins the 404 path: the server scopes deletes
// to the org and reports a foreign or dead id as not found, which for the
// caller means the job is already done and is not an error.
func TestLogoutAlreadyRevokedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "key_id": "k-dead"})
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "api key k-dead: not found")
		}
	}))
	defer deadSrv.Close()

	if err := saveKey(hostKey(normalizeBaseURL(deadSrv.URL)), "tk_dead"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}

	stdout, stderr, err := runLogout(envFrom(map[string]string{"TASKR_API": deadSrv.URL}))
	if err != nil {
		t.Fatalf("authLogoutCmd: %v", err)
	}
	if !strings.Contains(stdout, "already revoked") {
		t.Errorf("stdout missing already-revoked message:\n%s", stdout)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("stderr carried a warning for an already-revoked key:\n%s", stderr)
	}
}
