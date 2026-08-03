package chat

import (
	"slices"
	"testing"
)

func TestFolderActions(t *testing.T) {
	tests := []struct {
		input      string
		actionType string
		folder     string
		ok         bool
	}{
		{"create folder projects/athena", "create_folder", "projects/athena", true},
		{"Please make a folder called Work", "create_folder", "Work", true},
		{"remove the folder `old notes`", "delete_folder", "old notes", true},
		{"delete folder archive.", "delete_folder", "archive", true},
		{"make me a folder for tehranspeaker", "create_folder", "tehranspeaker", true},
		{"remove \"projects\" folder in \"work\"", "delete_folder", "work/projects", true},
		{"create a folder for my work and move notes into it", "", "", false},
		{"what folders do I have?", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			actions, ok := folderActions(tt.input)
			if ok != tt.ok {
				t.Fatalf("folderActions(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if !ok {
				return
			}
			if len(actions) != 1 || actions[0].Type != tt.actionType || actions[0].Folder != tt.folder {
				t.Fatalf("folderActions(%q) = %+v, want type=%q folder=%q", tt.input, actions, tt.actionType, tt.folder)
			}
		})
	}
}

func TestFolderActionsRenamesNestedFolder(t *testing.T) {
	actions, ok := folderActions("rename \"projects\" folder in \"work\" to \"clients\"")
	if !ok || len(actions) != 1 || actions[0].Type != "rename_folder" || actions[0].Folder != "work/projects" || actions[0].NewFolder != "clients" {
		t.Fatalf("folderActions returned %+v, %t", actions, ok)
	}
}

func TestCompoundFolderActions(t *testing.T) {
	tests := []struct {
		input string
		paths []string
		ok    bool
	}{
		{
			input: "make me a folder for work and a folder inside it Rumera and another folder in work projects",
			paths: []string{"work", "work/Rumera", "work/projects"},
			ok:    true,
		},
		{
			input: "create folder projects and a folder inside it athena",
			paths: []string{"projects", "projects/athena"},
			ok:    true,
		},
		{
			input: "make me a folder for work and a folder inside it Rumera and another folder for tehranspeaker and a folder called hospital",
			paths: []string{"work", "work/Rumera", "tehranspeaker", "hospital"},
			ok:    true,
		},
		{
			input: "create a folder for work and move notes into it",
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			actions, ok := folderActions(tt.input)
			if ok != tt.ok {
				t.Fatalf("folderActions(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if !ok {
				return
			}
			if len(actions) != 1 || actions[0].Type != "ensure_folders" {
				t.Fatalf("folderActions(%q) = %+v, want one ensure_folders action", tt.input, actions)
			}
			if got := actions[0].Paths; !slices.Equal(got, tt.paths) {
				t.Fatalf("folderActions(%q) paths = %v, want %v", tt.input, got, tt.paths)
			}
		})
	}
}

func TestIsListingRequest(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"what notes do I have?", true},
		{"list my notes", true},
		{"what notes do I have for books I haven't finished", false},
		{"show my notes in the reading folder", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isListingRequest(tt.input); got != tt.want {
				t.Fatalf("isListingRequest(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}

}
