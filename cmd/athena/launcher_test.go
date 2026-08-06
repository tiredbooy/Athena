package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindTUIEntryHonorsConfiguredPath(t *testing.T) {
	entry := filepath.Join(t.TempDir(), "index.js")
	if err := os.WriteFile(entry, []byte("// test entry"), 0o600); err != nil {
		t.Fatalf("write test entry: %v", err)
	}
	t.Setenv("ATHENA_TUI_ENTRY", entry)

	got, ok := findTUIEntry()
	if !ok || got != entry {
		t.Fatalf("findTUIEntry() = (%q, %v), want (%q, true)", got, ok, entry)
	}
}

func TestNodePathHonorsConfiguredCommand(t *testing.T) {
	t.Setenv("ATHENA_NODE", "node")
	got, err := nodePath()
	if err != nil {
		t.Fatalf("nodePath(): %v", err)
	}
	if got == "" {
		t.Fatal("nodePath() returned an empty command")
	}
}
