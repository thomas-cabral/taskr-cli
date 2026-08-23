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
	if hosts.Hosts["127.0.0.1:8099"].Key != "tk_local" {
		t.Errorf("local key = %q, want tk_local", hosts.Hosts["127.0.0.1:8099"].Key)
	}
	if hosts.Hosts["taskr.example.com"].Key != "tk_hosted" {
		t.Errorf("hosted key = %q, want tk_hosted", hosts.Hosts["taskr.example.com"].Key)
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
	if hosts.Hosts[host].Key != "tk_secret" {
		t.Errorf("stored key for %s = %q, want tk_secret", host, hosts.Hosts[host].Key)
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
	if _, saved := hosts.Hosts[hostKey(srv.URL)]; saved {
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
	if hosts.Hosts["127.0.0.1:8099"].Key != "tk_local" {
		t.Errorf("key = %q, want tk_local — the warning must not stop the read", hosts.Hosts["127.0.0.1:8099"].Key)
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
	if len(hosts.Hosts) != 0 {
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
// stands while Current is unset — which is now reachable only from a
// hand-edited or pre-Current file, since saveKey itself always names the
// host it just saved as Current (see the saveKey-driven tests below for
// that path).
func TestResolveTargetStaysOnTheDefaultWhenSeveralHostsAreLoggedIn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"hosts":{"100.91.91.47:8110":{"key":"tk_one"},"taskr.example.com":{"key":"tk_two"}}}`
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
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

func TestLegacyFlatHostsFileStillParses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "taskr", "hosts.json")
	if err := os.WriteFile(path, []byte(`{"one.example":{"key":"k1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if got.Current != "" {
		t.Errorf("Current = %q, want empty for a legacy file", got.Current)
	}
	if got.Hosts["one.example"].Key != "k1" {
		t.Errorf("Hosts[one.example].Key = %q, want k1", got.Hosts["one.example"].Key)
	}
}

func TestSavingRewritesALegacyFileIntoTheCurrentShape(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "taskr", "hosts.json")
	if err := os.WriteFile(path, []byte(`{"one.example":{"key":"k1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := saveKey("two.example", "k2"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var on struct {
		Current string               `json:"current"`
		Hosts   map[string]hostEntry `json:"hosts"`
	}
	if err := json.Unmarshal(data, &on); err != nil {
		t.Fatalf("re-reading saved file: %v", err)
	}
	if on.Current != "two.example" {
		t.Errorf("current = %q, want two.example", on.Current)
	}
	if on.Hosts["one.example"].Key != "k1" {
		t.Error("saving clobbered the pre-existing host")
	}
	if on.Hosts["two.example"].Key != "k2" {
		t.Error("saving did not store the new host")
	}
}

// This is TSK-60. Two hosts stored used to abandon both and fall through
// to the compiled default, which on a machine where something else holds
// that port answers 404 rather than failing to connect.
func TestTwoHostsResolveToTheCurrentOneNotTheDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"current":"two.example","hosts":{"one.example":{"key":"k1"},"two.example":{"key":"k2"}}}`
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveTarget(func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.BaseURL != "https://two.example" {
		t.Errorf("BaseURL = %q, want https://two.example", got.BaseURL)
	}
	if got.Key != "k2" {
		t.Errorf("Key = %q, want k2", got.Key)
	}
}

func TestEnvStillBeatsTheStoredCurrentHost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"current":"two.example","hosts":{"two.example":{"key":"k2"}}}`
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"TASKR_API": "one.example"}
	got, err := resolveTarget(func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.BaseURL != "https://one.example" {
		t.Errorf("BaseURL = %q, want https://one.example", got.BaseURL)
	}
}

// A current host stored without a scheme must still be reached over
// https. This is the property config.go's normalizeBaseURL comment
// describes: the key goes out on the first request, so an http:// guess
// leaks it before any redirect can intervene.
func TestAStoredCurrentHostWithNoSchemeGetsHTTPS(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"current":"taskr.example.com","hosts":{"taskr.example.com":{"key":"k"}}}`
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveTarget(func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.BaseURL != "https://taskr.example.com" {
		t.Errorf("BaseURL = %q, want https — a plaintext scheme leaks the key", got.BaseURL)
	}
}

func TestAStoredLocalCurrentHostStaysPlaintext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"current":"127.0.0.1:8099","hosts":{"127.0.0.1:8099":{"key":"k"}}}`
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveTarget(func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.BaseURL != "http://127.0.0.1:8099" {
		t.Errorf("BaseURL = %q, want http for loopback", got.BaseURL)
	}
}

// TestALegacyHostNamedHostsDoesNotShadowOtherHosts pins the fix-round-1
// finding: a legacy flat-map file that happens to have a host literally
// named "hosts" must not be misread as the modern shape and silently drop
// every other host in the file. The disambiguation goes by the top-level
// key set — "real.example" is not "current" or "hosts", so this whole file
// is legacy — not by whether "hosts" decodes to something non-nil.
func TestALegacyHostNamedHostsDoesNotShadowOtherHosts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "taskr", "hosts.json")
	if err := os.WriteFile(path, []byte(`{"hosts":{},"real.example":{"key":"k2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if got.Hosts["real.example"].Key != "k2" {
		t.Errorf("Hosts[real.example].Key = %q, want k2 — a host literally named \"hosts\" must not shadow it",
			got.Hosts["real.example"].Key)
	}
}

// TestModernHostsKeyWithNoOtherEntriesReadsAsEmpty covers the other side of
// that disambiguation: a file whose only top-level keys are "current"
// and/or "hosts" is modern even when "hosts" is an empty object — there is
// nothing outside the allowed key set to make it legacy.
func TestModernHostsKeyWithNoOtherEntriesReadsAsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(`{"hosts":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if len(got.Hosts) != 0 {
		t.Errorf("Hosts = %v, want empty", got.Hosts)
	}
}

// TestNullHostsDoesNotFabricateAPhantomHost pins the other reviewer finding
// from fix round 1: {"hosts":null} used to be misread as legacy (since
// modern.Hosts came back nil) and then, because unmarshaling JSON null into
// a struct map value is a no-op rather than an error, produced a phantom
// host literally named "hosts" with an empty key. The key-set
// disambiguation reads this as modern instead, so Hosts is simply empty.
func TestNullHostsDoesNotFabricateAPhantomHost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(`{"hosts":null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if len(got.Hosts) != 0 {
		t.Errorf("Hosts = %v, want empty — null hosts must not fabricate an entry named \"hosts\"", got.Hosts)
	}
}

// TestCurrentWithNullHostsDoesNotFail pins the last reviewer finding:
// {"current":"x","hosts":null} used to hard-fail, because falling through
// to the legacy parse tried to unmarshal the string "x" into a hostEntry
// struct. The key-set disambiguation reads this as modern, so it succeeds
// with Current set and an empty host map.
func TestCurrentWithNullHostsDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(`{"current":"x","hosts":null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadHosts(io.Discard)
	if err != nil {
		t.Fatalf("loadHosts: %v", err)
	}
	if got.Current != "x" {
		t.Errorf("Current = %q, want x", got.Current)
	}
	if len(got.Hosts) != 0 {
		t.Errorf("Hosts = %v, want empty", got.Hosts)
	}
}

// TestResolveTargetWarnsWhenSeveralHostsHaveNoCurrent pins fix-round-1
// finding 2: a file with several hosts and no Current used to fall back to
// the compiled default in total silence, which is TSK-60 verbatim against
// a real config that predates this fix. The warning must name the stored
// hosts and the remedy.
func TestResolveTargetWarnsWhenSeveralHostsHaveNoCurrent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"hosts":{"one.example":{"key":"k1"},"two.example":{"key":"k2"}}}`
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var warned bytes.Buffer
	target, err := resolveTarget(envFrom(nil), &warned)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if target.BaseURL != defaultAPI {
		t.Errorf("BaseURL = %q, want the default %q", target.BaseURL, defaultAPI)
	}
	got := warned.String()
	if got == "" {
		t.Fatal("no warning for several stored hosts with none marked current")
	}
	for _, want := range []string{"one.example", "two.example", "taskr auth login", "TASKR_API"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning = %q, want it to contain %q", got, want)
		}
	}
}

// TestResolveTargetDoesNotWarnWithASingleLoggedInHost pins the other half:
// exactly one stored host is unambiguous, so it must stay silent — a
// warning that fires in ordinary use is a warning nobody reads.
func TestResolveTargetDoesNotWarnWithASingleLoggedInHost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveKey("100.91.91.47:8110", "tk_only"); err != nil {
		t.Fatalf("saveKey: %v", err)
	}

	var warned bytes.Buffer
	if _, err := resolveTarget(envFrom(nil), &warned); err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if warned.Len() != 0 {
		t.Errorf("warned with a single unambiguous logged-in host: %q", warned.String())
	}
}

// TestResolveTargetDoesNotWarnWhenCurrentIsSet pins the same silence for
// several stored hosts once Current names one of them — the ambiguity the
// warning exists for is already resolved.
func TestResolveTargetDoesNotWarnWhenCurrentIsSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "taskr"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"current":"two.example","hosts":{"one.example":{"key":"k1"},"two.example":{"key":"k2"}}}`
	if err := os.WriteFile(filepath.Join(dir, "taskr", "hosts.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var warned bytes.Buffer
	if _, err := resolveTarget(envFrom(nil), &warned); err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if warned.Len() != 0 {
		t.Errorf("warned even though Current names a host: %q", warned.String())
	}
}

// TestResolveTargetDoesNotWarnWithoutAConfigFile pins that never having
// logged in at all is not ambiguity either.
func TestResolveTargetDoesNotWarnWithoutAConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var warned bytes.Buffer
	if _, err := resolveTarget(envFrom(nil), &warned); err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if warned.Len() != 0 {
		t.Errorf("warned with no config file at all: %q", warned.String())
	}
}
