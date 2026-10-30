package cli

import (
	"strings"
	"testing"
)

// The hint names the binary as it was invoked, so it can be pasted: it was
// hard-coded to identity-server, from when config ran inside it, and a bare
// name would not be on PATH for a binary under /opt.
func TestRestoreHintNamesTheRunningBinaryAsInvoked(t *testing.T) {
	for _, argv0 := range []string{
		"/opt/config/bin/config-server",
		"./identity-server",
		"countinghouse",
	} {
		if got := restoreHint(argv0); !strings.Contains(got, argv0+" --restore-backup <key>") {
			t.Errorf("restoreHint(%q) = %q, want it to contain %q", argv0, got, argv0+" --restore-backup <key>")
		}
	}
}

// Restoring under a running service corrupts its WAL, so the hint says to
// stop it first.
func TestRestoreHintSaysToStopTheServiceFirst(t *testing.T) {
	if got := restoreHint("/opt/config/bin/config-server"); !strings.Contains(got, "stop the service first") {
		t.Errorf("restoreHint = %q, want it to say to stop the service first", got)
	}
}
