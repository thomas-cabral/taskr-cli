// cli/config_test.go
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestResolveTargetUsesTASKR_APIForTheBaseURL pins the client's base URL
// resolution: TASKR_API selects the host, and a bare host or a full URL
// both work.
func TestResolveTargetUsesTASKR_APIForTheBaseURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	target, err := resolveTarget(envFrom(map[string]string{"TASKR_API": "http://127.0.0.1:9999"}), io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.BaseURL != "http://127.0.0.1:9999" {
		t.Errorf("BaseURL = %q, want http://127.0.0.1:9999", target.BaseURL)
	}
	if target.Host != "127.0.0.1:9999" {
		t.Errorf("Host = %q, want 127.0.0.1:9999", target.Host)
	}
}

func TestResolveTargetDefaultsWhenTASKR_APIUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	target, err := resolveTarget(envFrom(nil), io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.BaseURL != defaultAPI {
		t.Errorf("BaseURL = %q, want default %q", target.BaseURL, defaultAPI)
	}
}

// TestTASKR_KEYOverridesTheStoredKey pins the precedence design point 3
// requires: env wins over the file, matching gh's headless-use precedent.
func TestTASKR_KEYOverridesTheStoredKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := saveKey("127.0.0.1:8099", "tk_from_file"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}

	target, err := resolveTarget(envFrom(map[string]string{
		"TASKR_API": "http://127.0.0.1:8099",
		"TASKR_KEY": "tk_from_env",
	}), io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.Key != "tk_from_env" {
		t.Errorf("Key = %q, want the env override tk_from_env", target.Key)
	}
}

func TestResolveTargetFallsBackToStoredKeyWhenTASKR_KEYUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := saveKey("127.0.0.1:8099", "tk_from_file"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}

	target, err := resolveTarget(envFrom(map[string]string{"TASKR_API": "http://127.0.0.1:8099"}), io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.Key != "tk_from_file" {
		t.Errorf("Key = %q, want the stored key tk_from_file", target.Key)
	}
}

// TestHostsFileIsKeyedByHostAndDoesNotClobberOtherHosts pins design point
// 2: two hosts can be logged in to independently, mirroring gh's hosts.yml.
func TestHostsFileIsKeyedByHostAndDoesNotClobberOtherHosts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := saveKey("127.0.0.1:8099", "tk_local"); err != nil {
		t.Fatalf("saveKey local: %v", err)
	}
	if err := saveKey("taskr.example.com", "tk_hosted"); err != nil {
		t.Fatalf("saveKey hosted: %v", err)
	}

	hosts, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if hosts["127.0.0.1:8099"].Key != "tk_local" {
		t.Errorf("local key = %q, want tk_local", hosts["127.0.0.1:8099"].Key)
	}
	if hosts["taskr.example.com"].Key != "tk_hosted" {
		t.Errorf("hosted key = %q, want tk_hosted", hosts["taskr.example.com"].Key)
	}

	path := filepath.Join(dir, "taskr", "hosts.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat hosts.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("hosts.json mode = %o, want 0600", perm)
	}
}

// TestAuthLoginReadsKeyFromStdinNotArgv pins design point in the plan: the
// key is never accepted as a flag or positional argument, only piped in —
// argv would leave it in shell history and visible in `ps`.
//
// The fake server here answers GET /api/auth/status rather than
// POST /api/auth/login: browser-auth deletes the key-for-cookie exchange, so
// Client.Login now confirms a key by presenting it as X-Taskr-Key against
// status instead of posting it in a login body — see Login's own doc
// comment in client.go.
func TestAuthLoginReadsKeyFromStdinNotArgv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	var sawKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("X-Taskr-Key")
		json.NewEncoder(w).Encode(map[string]any{"authenticated": sawKey != "", "required": true})
	}))
	defer srv.Close()

	getenv := envFrom(map[string]string{"TASKR_API": srv.URL})
	var stdout, stderr bytes.Buffer
	err := authLogin(strings.NewReader("tk_secret\n"), nil, &stdout, &stderr, getenv)
	if err != nil {
		t.Fatalf("authLogin: %v (stderr: %s)", err, stderr.String())
	}
	if sawKey != "tk_secret" {
		t.Errorf("server saw X-Taskr-Key %q, want tk_secret", sawKey)
	}

	hosts, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	host := hostKey(srv.URL)
	if hosts[host].Key != "tk_secret" {
		t.Errorf("stored key for %s = %q, want tk_secret", host, hosts[host].Key)
	}
}

// TestAuthLoginRejectsAKeyTheServerDoesNotAccept pins the other half: a key
// the target host reports as unauthenticated must not be saved.
func TestAuthLoginRejectsAKeyTheServerDoesNotAccept(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"authenticated": false, "required": true})
	}))
	defer srv.Close()

	getenv := envFrom(map[string]string{"TASKR_API": srv.URL})
	var stdout, stderr bytes.Buffer
	if err := authLogin(strings.NewReader("tk_bad\n"), nil, &stdout, &stderr, getenv); err == nil {
		t.Fatal("authLogin with a rejected key succeeded, want an error")
	}

	hosts, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if _, saved := hosts[hostKey(srv.URL)]; saved {
		t.Error("a rejected key was saved to hosts.json")
	}
}

func TestAuthLoginRejectsAnEmptyKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	getenv := envFrom(map[string]string{"TASKR_API": "http://127.0.0.1:1"})
	var stdout, stderr bytes.Buffer
	if err := authLogin(strings.NewReader("   \n"), nil, &stdout, &stderr, getenv); err == nil {
		t.Fatal("authLogin with blank stdin succeeded, want an error")
	}
}

// TestLoadHostsWarnsAboutAReadableConfigFile pins the other half of the
// 0600 guarantee. saveHosts writes 0600, but nothing re-checked a file that
// already existed — restored from a backup, copied between machines, or
// created by hand — so a world-readable file holding a plaintext API key
// was used in silence.
func TestLoadHostsWarnsAboutAReadableConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := saveKey("127.0.0.1:8099", "tk_local"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}
	path := filepath.Join(dir, "taskr", "hosts.json")

	var quiet bytes.Buffer
	if _, err := loadHosts(&quiet); err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if quiet.Len() != 0 {
		t.Errorf("warned about a 0600 file: %q", quiet.String())
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	var warned bytes.Buffer
	hosts, err := loadHosts(&warned)
	if err != nil {
		t.Fatalf("loadHosts on a 0644 file: %v", err)
	}
	if hosts["127.0.0.1:8099"].Key != "tk_local" {
		t.Errorf("key = %q, want tk_local — the warning must not stop the read", hosts["127.0.0.1:8099"].Key)
	}
	got := warned.String()
	if got == "" {
		t.Fatal("no warning for a world-readable file holding a plaintext key")
	}
	for _, want := range []string{path, "chmod 600"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning = %q, want it to contain %q", got, want)
		}
	}
}

// TestLoadHostsWarnsAboutAGroupReadableConfigFile covers the mode a umask
// of 0022 leaves behind, which looks harmless and is not.
func TestLoadHostsWarnsAboutAGroupReadableConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := saveKey("127.0.0.1:8099", "tk_local"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "taskr", "hosts.json"), 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	var warned bytes.Buffer
	if _, err := loadHosts(&warned); err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if warned.Len() == 0 {
		t.Error("no warning for a group-readable file holding a plaintext key")
	}
}

// TestLoadHostsIsSilentWithoutAConfigFile pins that never having logged in
// is not a warning.
func TestLoadHostsIsSilentWithoutAConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var warned bytes.Buffer
	hosts, err := loadHosts(&warned)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("hosts = %v, want empty", hosts)
	}
	if warned.Len() != 0 {
		t.Errorf("warned with no config file at all: %q", warned.String())
	}
}

// TestResolveTargetAdoptsTheOnlyLoggedInHostWhenTASKR_APIUnset pins the
// point of `taskr auth login` on a machine where the default port is not
// where taskr lives: having logged in to exactly one host, a bare `taskr`
// from any directory must reach that host rather than the compiled-in
// default. Without this, the default silently wins and the CLI talks to
// whatever else happens to occupy 8099.
func TestResolveTargetAdoptsTheOnlyLoggedInHostWhenTASKR_APIUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := saveKey("100.91.91.47:8110", "tk_only"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}

	target, err := resolveTarget(envFrom(nil), io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.BaseURL != "http://100.91.91.47:8110" {
		t.Errorf("BaseURL = %q, want the only logged-in host http://100.91.91.47:8110", target.BaseURL)
	}
	if target.Host != "100.91.91.47:8110" {
		t.Errorf("Host = %q, want 100.91.91.47:8110", target.Host)
	}
	if target.Key != "tk_only" {
		t.Errorf("Key = %q, want tk_only", target.Key)
	}
}

// TestResolveTargetStaysOnTheDefaultWhenSeveralHostsAreLoggedIn pins the
// boundary of that inference: one host is unambiguous, several are not.
// Picking one of them by map order would be arbitrary, so the default
// stands until an explicit current-host key exists to name the winner.
func TestResolveTargetStaysOnTheDefaultWhenSeveralHostsAreLoggedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := saveKey("100.91.91.47:8110", "tk_one"); err != nil {
		t.Fatalf("saveKey one: %v", err)
	}
	if err := saveKey("taskr.example.com", "tk_two"); err != nil {
		t.Fatalf("saveKey two: %v", err)
	}

	target, err := resolveTarget(envFrom(nil), io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.BaseURL != defaultAPI {
		t.Errorf("BaseURL = %q, want the default %q while the current host is ambiguous", target.BaseURL, defaultAPI)
	}
}

// TestTASKR_APIWinsOverTheOnlyLoggedInHost pins that the new inference
// changes nothing about precedence: env still overrides the file, so a
// one-off against another instance stays a one-off.
func TestTASKR_APIWinsOverTheOnlyLoggedInHost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := saveKey("100.91.91.47:8110", "tk_only"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}

	target, err := resolveTarget(envFrom(map[string]string{"TASKR_API": "http://127.0.0.1:9999"}), io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.Host != "127.0.0.1:9999" {
		t.Errorf("Host = %q, want the env override 127.0.0.1:9999", target.Host)
	}
}
