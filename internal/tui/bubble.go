package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type SubmitFunc func(context.Context, string, func(string), func(string)) (string, error)
type ResetFunc func()
type answerMsg struct {
	text string
	err  error
}
type workerMsg struct {
	status, text string
	err          error
	done         bool
}

type bubbleModel struct {
	submit        SubmitFunc
	reset         ResetFunc
	input         textarea.Model
	output        viewport.Model
	lines         []string
	status        string
	busy          bool
	width, height int
	events        <-chan workerMsg
	cancel        context.CancelFunc
	spinner       spinner.Model
	hintIndex     int
	hintTicks     int
	commandIndex  int
	commandMenu   bool
	streaming     bool
	streamText    string
}

type commandSpec struct {
	name, description string
}

var (
	accent           = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	muted            = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	panel            = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("239")).Padding(0, 1)
	userMessage      = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	assistantMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	errorMessage     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	inputPrompt      = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	inputText        = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	inputPlaceholder = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	inputCursor      = lipgloss.Color("63")
)

var loadingHints = []string{
	"your vault stays local",
	"one model request is active",
	"you can press Esc to cancel",
	"Athena will report every action result",
}

var commands = []commandSpec{
	{"/clear", "clear the visible conversation"},
	{"/reset", "clear conversation and model history"},
	{"/help", "show commands and keyboard shortcuts"},
	{"/models", "show available chat models"},
	{"/model", "switch the chat model"},
	{"/theme", "choose midnight, ocean, or system"},
}

func setTheme(name string) string {
	switch strings.ToLower(name) {
	case "midnight":
		accent = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
		muted = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("239")).Padding(0, 1)
		userMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
		assistantMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		errorMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
		inputPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
		inputText = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
		inputPlaceholder = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		inputCursor = lipgloss.Color("63")
		return "midnight"
	case "ocean":
		accent = lipgloss.NewStyle().Foreground(lipgloss.Color("43")).Bold(true)
		muted = lipgloss.NewStyle().Foreground(lipgloss.Color("109"))
		panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("31")).Padding(0, 1)
		userMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("159"))
		assistantMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
		errorMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
		inputPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("43")).Bold(true)
		inputText = lipgloss.NewStyle().Foreground(lipgloss.Color("159"))
		inputPlaceholder = lipgloss.NewStyle().Foreground(lipgloss.Color("109"))
		inputCursor = lipgloss.Color("43")
		return "ocean"
	case "system":
		// ANSI slots are palette-relative, so these colors follow the user's
		// terminal theme while still giving the interface visual hierarchy.
		accent = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
		muted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("4")).Padding(0, 1)
		userMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
		assistantMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
		errorMessage = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		inputPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
		inputText = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
		inputPlaceholder = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		inputCursor = lipgloss.Color("6")
		return "system"
	default:
		return ""
	}
}

func applyInputTheme(input *textarea.Model) {
	styles := textarea.DefaultDarkStyles()
	// The default dark preset paints the empty rows black. That is useful for a
	// standalone editor, but looks like a broken block inside Athena's composer.
	// Keep the textarea transparent and let the composer panel provide its frame.
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.EndOfBuffer = lipgloss.NewStyle()
	styles.Focused.Prompt = inputPrompt
	styles.Focused.Text = inputText
	styles.Focused.Placeholder = inputPlaceholder
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	styles.Blurred.EndOfBuffer = lipgloss.NewStyle()
	styles.Blurred.Prompt = inputPrompt
	styles.Blurred.Text = inputText
	styles.Blurred.Placeholder = inputPlaceholder
	styles.Cursor.Color = inputCursor
	input.SetStyles(styles)
}

func RunBubble(submit SubmitFunc, reset ResetFunc) error {
	input := textarea.New()
	input.Placeholder = "Ask Athena…"
	input.ShowLineNumbers = false
	input.Prompt = "❯ "
	input.CharLimit = 10_000
	input.SetHeight(1)
	applyInputTheme(&input)
	input.Focus()
	spin := spinner.New()
	spin.Style = accent
	output := viewport.New()
	output.MouseWheelEnabled = true
	model := bubbleModel{submit: submit, reset: reset, input: input, output: output, status: "Ready", spinner: spin}
	_, err := tea.NewProgram(model, tea.WithContext(context.Background())).Run()
	return err
}

func (m bubbleModel) Init() tea.Cmd { return tea.Batch(textarea.Blink, m.spinner.Tick) }

func (m bubbleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(14, m.composerWidth()-4))
		m.output.SetWidth(max(20, msg.Width-6))
		m.output.SetHeight(max(4, msg.Height-12))
		m.refreshOutput()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.busy && m.cancel != nil {
				m.cancel()
				m.status = "Cancelling…"
			} else if m.commandMenu {
				m.commandMenu = false
				m.status = "Command menu dismissed"
			}
			return m, nil
		case "ctrl+l":
			if !m.busy {
				m.lines = nil
				m.status = "Conversation pane cleared"
				m.refreshOutput()
			}
			return m, nil
		case "home", "ctrl+home":
			if !m.busy && m.input.Value() == "" {
				m.output.GotoTop()
				return m, nil
			}
		case "pgup", "ctrl+u":
			if !m.busy {
				var cmd tea.Cmd
				m.output, cmd = m.output.Update(msg)
				return m, cmd
			}
		case "pgdown", "ctrl+d":
			if !m.busy {
				var cmd tea.Cmd
				m.output, cmd = m.output.Update(msg)
				return m, cmd
			}
		case "up":
			if matches := m.commandMatches(); len(matches) > 0 {
				m.commandIndex = (m.commandIndex - 1 + len(matches)) % len(matches)
				return m, nil
			}
		case "down":
			if matches := m.commandMatches(); len(matches) > 0 {
				m.commandIndex = (m.commandIndex + 1) % len(matches)
				return m, nil
			}
		case "tab":
			if matches := m.commandMatches(); len(matches) > 0 {
				m.input.SetValue(matches[m.commandIndex%len(matches)].name + " ")
				m.commandMenu = true
				return m, nil
			}
		case "enter":
			if m.busy || strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			input := strings.TrimSpace(m.input.Value())
			if matches := m.commandMatches(); m.commandMenu && len(matches) > 0 && input != matches[m.commandIndex%len(matches)].name {
				selected := matches[m.commandIndex%len(matches)].name
				if strings.HasPrefix(selected, "/theme ") {
					input = selected
				} else {
					m.input.SetValue(selected + " ")
					m.commandMenu = true
					return m, nil
				}
			}
			switch input {
			case "/clear":
				m.lines = nil
				m.status = "Conversation pane cleared"
				m.input.SetValue("")
				m.refreshOutput()
				return m, nil
			case "/reset":
				m.reset()
				m.lines = nil
				m.status = "Conversation and session reset"
				m.input.SetValue("")
				m.refreshOutput()
				return m, nil
			case "/help":
				m.lines = append(m.lines, renderAssistantMessage("Commands\n/clear — clear the visible pane\n/reset — clear pane and model history\n/help — show this help\n\nKeys: Enter send · Shift+Enter newline · Esc cancel · Ctrl+C quit"))
				m.input.SetValue("")
				m.refreshOutput()
				return m, nil
			case "/theme midnight", "/theme ocean", "/theme system":
				theme := setTheme(strings.TrimPrefix(input, "/theme "))
				applyInputTheme(&m.input)
				m.spinner.Style = accent
				m.status = "Theme: " + theme
				m.input.SetValue("")
				return m, nil
			case "/theme":
				m.input.SetValue("/theme ")
				m.commandMenu = true
				return m, nil
			}
			m.input.SetValue("")
			m.busy = true
			m.status = "Working…"
			m.lines = append(m.lines, renderUserMessage(input))
			m.refreshOutput()
			ctx, cancel := context.WithCancel(context.Background())
			m.cancel = cancel
			events := make(chan workerMsg, 16)
			m.events = events
			go func() {
				text, err := m.submit(ctx, input, func(status string) { events <- workerMsg{status: status} }, func(token string) { events <- workerMsg{text: token} })
				events <- workerMsg{text: text, err: err, done: true}
				close(events)
			}()
			return m, tea.Batch(waitForWorker(events), m.spinner.Tick)
		}
	case workerMsg:
		if !msg.done && msg.text == "" {
			m.status = msg.status
			return m, waitForWorker(m.events)
		}
		if !msg.done {
			if !m.streaming {
				m.lines = append(m.lines, renderAssistantMessage(""))
				m.streaming = true
				m.streamText = ""
			}
			m.streamText += msg.text
			m.lines[len(m.lines)-1] = renderAssistantMessage(RenderMarkdown(m.streamText))
			m.refreshOutput()
			return m, waitForWorker(m.events)
		}
		m.busy = false
		m.cancel = nil
		m.status = "Ready"
		if msg.err != nil {
			m.lines = append(m.lines, renderErrorMessage(msg.err.Error()))
		} else {
			entry := renderAssistantMessage(RenderMarkdown(msg.text))
			if m.streaming {
				m.lines[len(m.lines)-1] = entry
			} else {
				m.lines = append(m.lines, entry)
			}
		}
		m.streaming = false
		m.streamText = ""
		m.refreshOutput()
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.hintTicks++
		if m.hintTicks%24 == 0 {
			m.hintIndex = (m.hintIndex + 1) % len(loadingHints)
		}
		if m.busy {
			return m, cmd
		}
		return m, nil
	}
	if !m.busy {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			m.commandIndex = 0
			m.commandMenu = len(m.commandMatches()) > 0
		}
		return m, cmd
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

func (m bubbleModel) commandMatches() []commandSpec {
	input := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	if strings.HasPrefix(input, "/theme ") {
		choice := strings.TrimSpace(strings.TrimPrefix(input, "/theme "))
		var matches []commandSpec
		for _, theme := range []string{"midnight", "ocean", "system"} {
			if strings.HasPrefix(theme, choice) {
				matches = append(matches, commandSpec{"/theme " + theme, "apply the " + theme + " theme"})
			}
		}
		return matches
	}
	matches := make([]commandSpec, 0, len(commands))
	for _, command := range commands {
		if strings.HasPrefix(command.name, input) {
			matches = append(matches, command)
		}
	}
	return matches
}

func waitForWorker(events <-chan workerMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return workerMsg{done: true}
		}
		return msg
	}
}

func (m *bubbleModel) refreshOutput() {
	m.output.SetContent(strings.Join(m.lines, "\n\n"))
	m.output.GotoBottom()
}

// Chat entries use a restrained rail instead of full boxes. It gives each
// streamed response a stable visual boundary without consuming much terminal
// space or making long answers feel like a stack of UI panels.
func renderUserMessage(text string) string {
	return renderChatMessage(accent.Render("❯")+muted.Render(" You"), text, userMessage, accent)
}

func renderAssistantMessage(text string) string {
	return renderChatMessage(accent.Render("✦ Athena"), text, assistantMessage, accent)
}

func renderErrorMessage(text string) string {
	return renderChatMessage(errorMessage.Render("! Request failed"), text, errorMessage, errorMessage)
}

func renderChatMessage(header, text string, contentStyle, railStyle lipgloss.Style) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = railStyle.Render("│ ") + contentStyle.Render(line)
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func (m bubbleModel) composerWidth() int {
	return max(18, m.width-4)
}

func (m bubbleModel) composerView() string {
	width := m.composerWidth()
	return panel.Width(width).Render(m.input.View())
}

func (m bubbleModel) View() tea.View {
	body := m.output.View()
	if len(m.lines) == 0 {
		body = accent.Render("✦ Athena") + "\n" + muted.Render("Ask about your local vault. Type /help to see commands.")
	}
	header := accent.Render("✦ ATHENA") + muted.Render("  local knowledge workspace")
	status := muted.Render(m.status)
	if m.busy {
		status = m.spinner.View() + " " + status + muted.Render("  ·  "+loadingHints[m.hintIndex])
	}
	width := m.composerWidth()
	var suggestions string
	if matches := m.commandMatches(); m.commandMenu && len(matches) > 0 {
		selected := matches[m.commandIndex%len(matches)]
		suggestions = panel.Width(width).Render(accent.Render("› "+selected.name)+muted.Render("  "+selected.description)+muted.Render("   ↑↓ choose · Tab complete · Esc dismiss")) + "\n"
	}
	composer := m.composerView()
	content := header + "\n" + muted.Render(strings.Repeat("─", width)) + "\n\n" + body + "\n\n" + suggestions + composer + "\n" + status + "\n" + muted.Render("Enter send · Shift+Enter newline · ↑↓ select command · Tab complete · Esc cancel")
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "Athena"
	// Do not capture drag events: users must be able to select and copy text
	// with their terminal's normal mouse behavior. Keyboard scrolling remains
	// available through Home/PgUp/PgDn and their Ctrl alternatives.
	v.MouseMode = tea.MouseModeNone
	return v
}
