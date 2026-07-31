package chat

import "testing"

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
