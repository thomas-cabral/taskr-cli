// cli/device_test.go
package cli

import (
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
