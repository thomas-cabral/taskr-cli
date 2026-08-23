// cli/config.go
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultAPI is the base URL used when TASKR_API is unset, no host is
// marked current, and no single host is logged in — the hosted instance,
// so a freshly installed taskr works before it is configured. A local
// taskr-api is reached with TASKR_API=127.0.0.1:8099, which
// normalizeBaseURL keeps on http because the host is loopback.
const defaultAPI = "https://api.aitaskr.com"

// hostEntry is one host's stored credential.
type hostEntry struct {
	Key string `json:"key"`
}

// hostsFile is $XDG_CONFIG_HOME/taskr/hosts.json. Hosts is keyed by host,
// the same shape gh uses for hosts.yml, so a contributor can be
// authenticated to a local instance and a hosted one at once without one
// login clobbering the other. Current names which of them an invocation
// with no TASKR_API talks to.
//
// Current exists because its absence was a bug: with more than one host
// stored, resolveTarget could not choose and fell through to the compiled
// default, landing on whatever holds that port locally (TSK-60).
type hostsFile struct {
	Current string               `json:"current,omitempty"`
	Hosts   map[string]hostEntry `json:"hosts"`
}

// UnmarshalJSON accepts both the current shape and the flat map that
// predates Current, so an existing config keeps working untouched. The two
// are told apart by the top-level key set, not by whether "hosts" decodes
// to something non-nil: a file is modern only when "hosts" is present and
// every top-level key is "current" or "hosts". Anything else — including a
// legacy file that happens to have a host literally named "hosts" — is
// legacy, so a colliding key never shadows a real host and silently drops
// it. A legacy file is rewritten into the current shape the next time
// anything saves.
func (h *hostsFile) UnmarshalJSON(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	_, hasHosts := top["hosts"]
	modern := hasHosts
	for k := range top {
		if k != "current" && k != "hosts" {
			modern = false
			break
		}
	}
	if modern {
		var m struct {
			Current string               `json:"current"`
			Hosts   map[string]hostEntry `json:"hosts"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		h.Current, h.Hosts = m.Current, m.Hosts
		return nil
	}
	var legacy map[string]hostEntry
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	h.Current, h.Hosts = "", legacy
	return nil
}

// configPath resolves $XDG_CONFIG_HOME/taskr/hosts.json, falling back to
// ~/.config when XDG_CONFIG_HOME is unset.
func configPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "taskr", "hosts.json"), nil
}

// loadHosts reads the config file. A missing file is not an error — it
// means no host has ever been logged in — and reads back as an empty set.
//
// A file that already exists gets its mode re-checked, and anything the
// owner does not have to itself is reported to warn. saveHosts writes 0600,
// but nothing re-checked a file restored from a backup, copied between
// machines, or written by hand — so a readable file full of plaintext API
// keys was used in silence. It is a warning rather than a refusal: the key
// is already exposed, and failing here would only lock the owner out of
// fixing it.
func loadHosts(warn io.Writer) (hostsFile, error) {
	path, err := configPath()
	if err != nil {
		return hostsFile{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hostsFile{Hosts: map[string]hostEntry{}}, nil
	}
	if err != nil {
		return hostsFile{}, err
	}
	warnIfReadable(warn, path)
	var h hostsFile
	if err := json.Unmarshal(data, &h); err != nil {
		return hostsFile{}, fmt.Errorf("taskr: parsing %s: %w", path, err)
	}
	if h.Hosts == nil {
		h.Hosts = map[string]hostEntry{}
	}
	return h, nil
}

// warnAmbiguousHosts reports that resolveTarget could not choose among
// several stored hosts and fell back to the compiled default, because none
// of them is marked current. This is TSK-60's remaining edge: saveKey
// naming the just-logged-in host as current only helps someone who logs in
// again, so a file that already has several hosts stays broken with no
// clue why unless this warns about it. Warn and continue, matching
// warnIfReadable's pattern — the file is still fully usable, it just needs
// a current host named.
func warnAmbiguousHosts(warn io.Writer, hosts map[string]hostEntry) {
	if warn == nil {
		return
	}
	names := make([]string, 0, len(hosts))
	for host := range hosts {
		names = append(names, host)
	}
	sort.Strings(names)
	fmt.Fprintf(warn, "taskr: warning: %d hosts are stored (%s) and none is marked current, "+
		"so falling back to %s. Run `taskr auth login` against the one you want, or set TASKR_API.\n",
		len(names), strings.Join(names, ", "), defaultAPI)
}

// warnIfReadable reports a config file any account but the owner can read.
func warnIfReadable(warn io.Writer, path string) {
	if warn == nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	perm := info.Mode().Perm()
	if perm&0o077 == 0 {
		return
	}
	fmt.Fprintf(warn, "taskr: warning: %s is mode %04o and holds plaintext API keys; "+
		"anyone with an account on this machine can read them. Fix it with `chmod 600 %s`.\n",
		path, perm, path)
}

// saveHosts writes the config file at mode 0600 — it holds plaintext API
// keys, so it must never be group- or world-readable.
func saveHosts(h hostsFile) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// hostKey returns the host:port component of a base URL — the form hosts.json
// keys entries by, and the form a bare TASKR_API value (no scheme) already is.
func hostKey(baseURL string) string {
	u := normalizeBaseURL(baseURL)
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	return strings.TrimSuffix(u, "/")
}

// normalizeBaseURL adds a scheme when the caller gave a bare host, so
// TASKR_API=127.0.0.1:8099 and TASKR_API=http://127.0.0.1:8099 both work.
//
// A bare host defaults to https unless it is plainly local. This used to
// prepend http:// unconditionally, which put a live API key on the wire in
// cleartext for every call to a bare public hostname -- the CLI sends
// X-Taskr-Key on the FIRST request, so an ingress-side redirect does not
// save it; by the time the 301 comes back the key has already been sent.
//
// An explicit scheme is always honoured. Someone who types http:// against a
// public host has said what they meant, and this is not the layer to argue.
func normalizeBaseURL(v string) string {
	if v == "" {
		return defaultAPI
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return strings.TrimSuffix(v, "/")
	}
	bare := strings.TrimSuffix(v, "/")
	if isLocalHost(bare) {
		return "http://" + bare
	}
	return "https://" + bare
}

// isLocalHost reports whether a bare host:port is somewhere plaintext is
// reasonable: loopback, an RFC1918 network, or the CGNAT range Tailscale
// hands out. Everything else is treated as the public internet.
//
// Deliberately errs toward https: an unparseable or unfamiliar host gets the
// secure scheme, so the failure mode is a connection error the caller can
// see and correct, not a silently leaked credential.
func isLocalHost(hostPort string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")

	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A name, not an address. Bare single-label names are LAN-ish
		// ("taskr-box:8099"); anything with a dot is a real domain.
		return !strings.Contains(host, ".")
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return true
	}
	// 100.64.0.0/10 -- Tailscale's addresses live here and are not covered by
	// IsPrivate, which only knows the RFC1918 blocks.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

// resolvedTarget is the host this invocation talks to, plus the credential
// for it.
type resolvedTarget struct {
	BaseURL string
	Host    string
	Key     string
}

// resolveTarget applies the precedence the whole CLI follows: TASKR_API
// selects the host, TASKR_KEY overrides whatever hosts.json has stored for
// it. Env wins over file in every case, matching gh's headless-use
// precedent.
//
// With TASKR_API unset, the stored Current host wins when one is named —
// saveKey sets it to whatever host was just logged in to. Without a
// Current (a single logged-in host, or a file from before Current
// existed), a lone logged-in host is still taken as the host to use:
// logging in to exactly one instance and then being sent to the
// compiled-in default anyway is never what the caller meant, and on a
// machine where something else already holds the default port, that lands
// the CLI on an unrelated service that answers 404 rather than on a clean
// connection error. Several hosts with no Current stay ambiguous and keep
// the default, because picking one by map order would be arbitrary
// (TSK-60) — but that fallback is now warned about, naming the stored
// hosts and the remedy, since falling back in silence is exactly the bug
// this file closes.
func resolveTarget(getenv func(string) string, warn io.Writer) (resolvedTarget, error) {
	envAPI, envKey := getenv("TASKR_API"), getenv("TASKR_KEY")

	// Read the file unless the caller already supplied both halves.
	var hosts hostsFile
	if envAPI == "" || envKey == "" {
		var err error
		if hosts, err = loadHosts(warn); err != nil {
			return resolvedTarget{}, err
		}
	}

	base := normalizeBaseURL(envAPI)
	if envAPI == "" {
		switch {
		case hosts.Current != "":
			base = normalizeBaseURL(hosts.Current)
		case len(hosts.Hosts) == 1:
			for stored := range hosts.Hosts {
				base = normalizeBaseURL(stored)
			}
		case len(hosts.Hosts) > 1:
			warnAmbiguousHosts(warn, hosts.Hosts)
		}
	}
	host := hostKey(base)

	key := envKey
	if key == "" {
		key = hosts.Hosts[host].Key
	}
	return resolvedTarget{BaseURL: base, Host: host, Key: key}, nil
}

// saveKey stores key for host, leaving every other host's entry untouched.
// It reads with warnings discarded: the caller has already been through
// resolveTarget, so a mode warning here would only be the second copy —
// and saveHosts is about to write 0600 anyway.
func saveKey(host, key string) error {
	hosts, err := loadHosts(io.Discard)
	if err != nil {
		return err
	}
	hosts.Hosts[host] = hostEntry{Key: key}
	hosts.Current = host
	return saveHosts(hosts)
}
