// cli/config.go
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// defaultAPI is the base URL used when TASKR_API is unset and no single
// host is logged in — a local taskr-api on its default port.
const defaultAPI = "http://127.0.0.1:8099"

// hostEntry is one host's stored credential.
type hostEntry struct {
	Key string `json:"key"`
}

// hostsFile is $XDG_CONFIG_HOME/taskr/hosts.json, keyed by host — the same
// shape gh uses for hosts.yml, so a contributor can be authenticated to a
// local instance and a hosted one at once without one login clobbering the
// other.
type hostsFile map[string]hostEntry

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
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hostsFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	warnIfReadable(warn, path)
	var h hostsFile
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("taskr: parsing %s: %w", path, err)
	}
	if h == nil {
		h = hostsFile{}
	}
	return h, nil
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
// With TASKR_API unset, a single logged-in host is taken as the host to
// use. Logging in to exactly one instance and then being sent to the
// compiled-in default anyway is never what the caller meant — and on a
// machine where something else already holds the default port, that lands
// the CLI on an unrelated service that answers 404 rather than on a clean
// connection error. Several logged-in hosts stay ambiguous and keep the
// default, because picking one by map order would be arbitrary; naming a
// winner needs an explicit current-host key this file does not have yet.
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
	if envAPI == "" && len(hosts) == 1 {
		for stored := range hosts {
			base = normalizeBaseURL(stored)
		}
	}
	host := hostKey(base)

	key := envKey
	if key == "" {
		key = hosts[host].Key
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
	hosts[host] = hostEntry{Key: key}
	return saveHosts(hosts)
}
