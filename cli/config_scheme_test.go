// cli/config_scheme_test.go
package cli

import "testing"

// A bare TASKR_API used to get http:// unconditionally. Against a public
// hostname that put a live API key on the wire in cleartext on every call --
// and an ingress redirect cannot undo it, because the CLI sends X-Taskr-Key
// on the first request, before any 301 comes back.
func TestBareHostDefaultsToHTTPS(t *testing.T) {
	public := []string{
		"taskr-api.flowstatetechsolutions.com",
		"taskr-api.flowstatetechsolutions.com:8443",
		"example.com",
		"8.8.8.8",
		"8.8.8.8:8099",
	}
	for _, host := range public {
		got := normalizeBaseURL(host)
		if got[:8] != "https://" {
			t.Errorf("normalizeBaseURL(%q) = %q, want an https:// URL — a key sent here goes out in the clear", host, got)
		}
	}
}

func TestBareLocalHostStaysPlaintext(t *testing.T) {
	// Forcing https here would break every local dev loop and the tailnet
	// bind, neither of which terminates TLS.
	local := []string{
		"127.0.0.1:8099",
		"localhost:8099",
		"localhost",
		"[::1]:8099",
		"192.168.1.50:8099",
		"10.0.0.4:8099",
		"172.16.5.5:8099",
		"100.91.91.47:8110", // tailscale CGNAT, not covered by IsPrivate
		"taskr-box:8099",    // single-label LAN name
	}
	for _, host := range local {
		got := normalizeBaseURL(host)
		if got[:7] != "http://" {
			t.Errorf("normalizeBaseURL(%q) = %q, want http:// — this host cannot terminate TLS", host, got)
		}
	}
}

// An explicit scheme is the caller saying what they meant, including when
// what they meant is plaintext against a public host.
func TestAnExplicitSchemeIsHonoured(t *testing.T) {
	cases := map[string]string{
		"http://example.com":       "http://example.com",
		"https://example.com":      "https://example.com",
		"http://127.0.0.1:8099":    "http://127.0.0.1:8099",
		"https://example.com/":     "https://example.com",
		"http://taskr-box:8099///": "http://taskr-box:8099//",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// hostKey keys hosts.json and must not start returning a different key for
// the same host now that the scheme it strips can differ.
func TestHostKeyIsUnchangedByTheSchemeDefault(t *testing.T) {
	cases := map[string]string{
		"taskr-api.flowstatetechsolutions.com":         "taskr-api.flowstatetechsolutions.com",
		"https://taskr-api.flowstatetechsolutions.com": "taskr-api.flowstatetechsolutions.com",
		"http://taskr-api.flowstatetechsolutions.com":  "taskr-api.flowstatetechsolutions.com",
		"127.0.0.1:8099":        "127.0.0.1:8099",
		"http://127.0.0.1:8099": "127.0.0.1:8099",
	}
	for in, want := range cases {
		if got := hostKey(in); got != want {
			t.Errorf("hostKey(%q) = %q, want %q — a changed key orphans the stored credential", in, got, want)
		}
	}
}
