package notes

import (
	"context"
	"testing"

	"github.com/tiredbooy/internal/ai"
	"github.com/tiredbooy/internal/utils"
)

func TestVerifyWriteAcceptsFolderColorActionWithoutNoteID(t *testing.T) {
	service, vault := graphService(t)
	for _, folder := range []string{"books/reading", "books/finished"} {
		if err := utils.EnsureDir(vault, folder); err != nil {
			t.Fatalf("create %s: %v", folder, err)
		}
	}
	if _, err := service.AddFolderGraphColors("books", true, ""); err != nil {
		t.Fatalf("add colors: %v", err)
	}

	err := service.VerifyWrite(context.Background(), ai.Action{
		Type:            "set_folder_colors",
		Folder:          "books",
		IncludeChildren: true,
	})
	if err != nil {
		t.Fatalf("verify folder colors: %v", err)
	}
}

func TestVerifyWriteChecksFolderRenameAndMoveDestinations(t *testing.T) {
	for _, test := range []struct {
		name   string
		action ai.Action
		apply  func(*Service) error
	}{
		{
			name:   "rename",
			action: ai.Action{Type: "rename_folder", Folder: "work/projects", NewFolder: "clients"},
			apply: func(service *Service) error {
				_, err := service.RenameFolder("work/projects", "clients")
				return err
			},
		},
		{
			name:   "move",
			action: ai.Action{Type: "move_folder", Folder: "work/projects", NewFolder: "archive"},
			apply: func(service *Service) error {
				_, err := service.MoveFolder("work/projects", "archive")
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, vault := graphService(t)
			for _, folder := range []string{"work/projects", "archive"} {
				if err := utils.EnsureDir(vault, folder); err != nil {
					t.Fatalf("create %s: %v", folder, err)
				}
			}
			if err := test.apply(service); err != nil {
				t.Fatalf("apply action: %v", err)
			}
			if err := service.VerifyWrite(context.Background(), test.action); err != nil {
				t.Fatalf("verify action: %v", err)
			}
		})
	}
}
