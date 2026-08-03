package utils

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListFoldersSkipsObsidianConfiguration(t *testing.T) {
	vault := t.TempDir()
	for _, folder := range []string{".obsidian/plugins", "work/projects"} {
		if err := os.MkdirAll(filepath.Join(vault, folder), 0o755); err != nil {
			t.Fatalf("create %s: %v", folder, err)
		}
	}

	folders, err := ListFolders(vault)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if !slices.Equal(folders, []string{"work", "work/projects"}) {
		t.Fatalf("folders = %v", folders)
	}
}
