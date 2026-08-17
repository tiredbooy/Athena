package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tiredbooy/internal/notes"
)

// F-06: the process used to run under a 3600s deadline, so a session that
// lasted an hour died mid-conversation. Only a turn is bounded now.
func TestProcessContextHasNoDeadlineAndEndsOnInterrupt(t *testing.T) {
	ctx, stop := processContext()
	defer stop()

	if deadline, ok := ctx.Deadline(); ok {
		t.Fatalf("process context expires at %v; long sessions would die", deadline)
	}

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find test process: %v", err)
	}
	if err := self.Signal(os.Interrupt); err != nil {
		t.Fatalf("send interrupt: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("interrupt did not cancel the process context")
	}
}

// V-07: a scan that only prints counts is indistinguishable from a scan that
// found nothing, so every file the startup reconcile refused to settle has to
// be named with its reason.
func TestReportVaultScanNamesEveryFlaggedFile(t *testing.T) {
	var out strings.Builder
	reportVaultScan(&out, notes.VaultScan{
		Added:       []string{"inbox/new.md"},
		Repaired:    []string{"work/moved.md"},
		Missing:     []notes.VaultIssue{{Path: "work/gone.md", Reason: "no matching file in the vault"}},
		Conflicting: []notes.VaultIssue{{Path: "work/edited.md", Reason: "edited outside Athena"}},
	})

	for _, want := range []string{
		"vault reconcile: 1 indexed, 1 repaired, 1 missing, 1 conflicting",
		"missing: work/gone.md — no matching file in the vault",
		"conflicting: work/edited.md — edited outside Athena",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report is missing %q:\n%s", want, out.String())
		}
	}
}

// A vault-wide move must not bury the rest of startup, and the user still has
// to be told the list was cut short.
func TestReportVaultScanCapsLongIssueLists(t *testing.T) {
	scan := notes.VaultScan{}
	for index := 0; index < 12; index++ {
		scan.Missing = append(scan.Missing, notes.VaultIssue{Path: fmt.Sprintf("note-%d.md", index), Reason: "gone"})
	}
	var out strings.Builder
	reportVaultScan(&out, scan)

	if strings.Contains(out.String(), "note-10.md") {
		t.Fatalf("report listed an 11th missing file:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "… and 2 more missing") {
		t.Fatalf("report did not say the list was cut short:\n%s", out.String())
	}
}

// Nothing to report prints nothing: a quiet startup is the normal case.
func TestReportVaultScanIsSilentWhenNothingChanged(t *testing.T) {
	var out strings.Builder
	reportVaultScan(&out, notes.VaultScan{})
	if out.Len() != 0 {
		t.Fatalf("clean scan printed %q", out.String())
	}
}
