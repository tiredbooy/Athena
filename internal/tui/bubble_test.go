package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

func TestRenderChatMessageWrapsToAvailableWidth(t *testing.T) {
	message := renderChatMessage("Athena", "a message that must wrap instead of being clipped by the viewport", lipgloss.NewStyle(), lipgloss.NewStyle(), 20)
	lines := strings.Split(message, "\n")
	if len(lines) < 3 {
		t.Fatalf("message did not wrap: %q", message)
	}
	for _, line := range lines[1:] {
		if width := lipgloss.Width(line); width > 20 {
			t.Fatalf("wrapped line width = %d, want <= 20: %q", width, line)
		}
	}
}

func TestRefreshOutputPreservesReaderPositionWhenNotFollowing(t *testing.T) {
	output := viewport.New()
	output.SetWidth(30)
	output.SetHeight(3)
	model := bubbleModel{
		output: output,
		lines: []chatMessage{
			{header: "You", text: strings.Repeat("first message ", 8)},
			{header: "Athena", text: strings.Repeat("second message ", 8)},
		},
		followOutput: true,
	}
	model.refreshOutput()
	model.output.GotoTop()
	model.followOutput = false
	model.lines = append(model.lines, chatMessage{header: "Athena", text: strings.Repeat("new message ", 8)})
	model.refreshOutput()
	if !model.output.AtTop() {
		t.Fatalf("viewport moved from the reader's position: offset=%d", model.output.YOffset())
	}
}

func TestApprovalStateUsesPendingPlanNotResponseText(t *testing.T) {
	for _, tt := range []struct {
		name    string
		pending bool
		want    bool
	}{
		{name: "model text without plan", pending: false, want: false},
		{name: "pending plan", pending: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := bubbleModel{pendingActions: func() bool { return tt.pending }}
			updated, _ := model.Update(workerMsg{done: true, text: "Review required — no changes have been made."})
			got := updated.(bubbleModel).reviewing
			if got != tt.want {
				t.Fatalf("reviewing = %t, want %t", got, tt.want)
			}
		})
	}
}
