package cli

import "testing"

// The hint names the binary that is running, whichever service it is: it
// was hard-coded to identity-server, from when config ran inside it.
func TestRestoreHintNamesTheRunningBinary(t *testing.T) {
	for argv0, want := range map[string]string{
		"/opt/config/bin/config-server-20261030-0ab6e31": "To restore: config-server-20261030-0ab6e31 --restore-backup <key>",
		"./identity-server": "To restore: identity-server --restore-backup <key>",
		"countinghouse":     "To restore: countinghouse --restore-backup <key>",
	} {
		if got := restoreHint(argv0); got != want {
			t.Errorf("restoreHint(%q) = %q, want %q", argv0, got, want)
		}
	}
}
