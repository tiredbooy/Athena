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
type PendingActionsFunc func() bool
type ModelOption struct {
	ProviderID, ProviderName, Model string
	Current                         bool
}
type ModelsFunc func(context.Context) ([]ModelOption, error)
type SelectModelFunc func(context.Context, ModelOption) (string, error)
type ConnectionInput struct{ Name, Type, BaseURL, APIKeyEnv, ChatModel string }
type ConnectFunc func(ConnectionInput) (string, error)
type SubscriptionFunc func(context.Context) (string, error)
type answerMsg struct {
	text string
	err  error
}
type workerMsg struct {
	requestID    uint64
	status, text string
	err          error
	done         bool
}
type modelPickerMsg struct {
	options []ModelOption
	err     error
}
type modelSelectedMsg struct {
	text string
	err  error
}
type providerConnectedMsg struct {
	text string
	err  error
}

type chatMessage struct {
	header, text            string
	contentStyle, railStyle lipgloss.Style
}

type bubbleModel struct {
	submit         SubmitFunc
	reset          ResetFunc
	pendingActions PendingActionsFunc
	models         ModelsFunc
	selectModel    SelectModelFunc
	connect        ConnectFunc
	subscription   SubscriptionFunc
	input          textarea.Model
	output         viewport.Model
	lines          []chatMessage
	status         string
	busy           bool
	width, height  int
	events         <-chan workerMsg
	cancel         context.CancelFunc
	spinner        spinner.Model
	hintIndex      int
	hintTicks      int
	commandIndex   int
	commandMenu    bool
	streaming      bool
	streamText     string
	followOutput   bool
	picker         bool
	pickerLoading  bool
	pickerOptions  []ModelOption
	pickerIndex    int
	connecting     bool
	connectType    string
	connectStep    int
	connectValues  []string
	reviewing      bool
	requestID      uint64
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
	{"/compact", "compact older conversation context"},
	{"/doctor", "diagnose vault, providers, and embeddings"},
	{"/models", "show available chat models"},
	{"/connect", "connect or add a chat provider"},
	{"/confirm", "apply the reviewed change plan"},
	{"/cancel", "discard the reviewed change plan"},
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

func RunBubble(submit SubmitFunc, reset ResetFunc, pendingActions PendingActionsFunc, models ModelsFunc, selectModel SelectModelFunc, connect ConnectFunc, subscription SubscriptionFunc) error {
	input := textarea.New()
	input.Placeholder = "Ask Athena…"
	input.ShowLineNumbers = false
	input.Prompt = "❯ "
	input.CharLimit = 10_000
	input.SetHeight(2)
	applyInputTheme(&input)
	input.Focus()
	spin := spinner.New()
	spin.Style = accent
	output := viewport.New()
	output.SoftWrap = true
	model := bubbleModel{submit: submit, reset: reset, pendingActions: pendingActions, models: models, selectModel: selectModel, connect: connect, subscription: subscription, input: input, output: output, status: "Ready", spinner: spin, followOutput: true}
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
	case tea.MouseWheelMsg:
		return m, nil
	case tea.KeyPressMsg:
		if m.reviewing && (msg.String() == "y" || msg.String() == "Y") {
			m.reviewing = false
			return m, m.startSubmit("/confirm")
		}
		if m.reviewing && (msg.String() == "n" || msg.String() == "N") {
			m.reviewing = false
			return m, m.startSubmit("/cancel")
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.reviewing {
				m.reviewing = false
				return m, m.startSubmit("/cancel")
			} else if m.busy && m.cancel != nil {
				m.cancel()
				m.requestID++ // Late results from this request must not alter the UI.
				m.busy, m.cancel, m.streaming = false, nil, false
				m.status = "Cancelled"
			} else if m.picker || m.connecting {
				m.closeOverlay()
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
			if m.input.Value() == "" {
				m.output.GotoTop()
				m.followOutput = false
				return m, nil
			}
		case "pgup", "ctrl+u":
			var cmd tea.Cmd
			m.output, cmd = m.output.Update(msg)
			m.followOutput = m.output.AtBottom()
			return m, cmd
		case "pgdown", "ctrl+d":
			var cmd tea.Cmd
			m.output, cmd = m.output.Update(msg)
			m.followOutput = m.output.AtBottom()
			return m, cmd
		case "up":
			if m.picker && len(m.pickerOptions) > 0 {
				count := len(m.filteredModels()) + 1
				m.pickerIndex = (m.pickerIndex - 1 + count) % count
				return m, nil
			}
			if m.connecting && m.connectStep == 0 {
				m.pickerIndex = (m.pickerIndex + 1) % len(connectChoices())
				return m, nil
			}
			if matches := m.commandMatches(); len(matches) > 0 {
				m.commandIndex = (m.commandIndex - 1 + len(matches)) % len(matches)
				return m, nil
			}
			if m.input.Value() == "" {
				m.output.ScrollUp(1)
				m.followOutput = m.output.AtBottom()
				return m, nil
			}
		case "down":
			if m.picker && len(m.pickerOptions) > 0 {
				count := len(m.filteredModels()) + 1
				m.pickerIndex = (m.pickerIndex + 1) % count
				return m, nil
			}
			if m.connecting && m.connectStep == 0 {
				m.pickerIndex = (m.pickerIndex + 1) % len(connectChoices())
				return m, nil
			}
			if matches := m.commandMatches(); len(matches) > 0 {
				m.commandIndex = (m.commandIndex + 1) % len(matches)
				return m, nil
			}
			if m.input.Value() == "" {
				m.output.ScrollDown(1)
				m.followOutput = m.output.AtBottom()
				return m, nil
			}
		case "tab":
			if matches := m.commandMatches(); len(matches) > 0 {
				m.input.SetValue(matches[m.commandIndex%len(matches)].name + " ")
				m.commandMenu = true
				return m, nil
			}
		case "enter":
			if m.reviewing {
				m.reviewing = false
				return m, m.startSubmit("/confirm")
			}
			if m.busy {
				if strings.TrimSpace(m.input.Value()) != "" {
					m.status = "Athena is still working — your draft is kept below. Press Esc to cancel the current request."
				}
				return m, nil
			}
			if m.picker {
				if m.pickerLoading {
					return m, nil
				}
				matches := m.filteredModels()
				if m.pickerIndex > len(matches) {
					m.pickerIndex = len(matches)
				}
				if m.pickerIndex == len(matches) {
					return m.beginConnect(), nil
				}
				selected := matches[m.pickerIndex]
				m.status = "Selecting " + selected.Model
				return m, selectModelCmd(m.selectModel, selected)
			}
			if m.connecting {
				if m.connectStep == 0 {
					preset := connectChoices()[m.pickerIndex]
					if preset.kind == "openai_subscription" {
						m.status = "Preparing ChatGPT sign-in…"
						return m, subscriptionCmd(m.subscription)
					}
					if preset.kind == "ollama" {
						m.closeOverlay()
						return m, connectCmd(m.connect, ConnectionInput{Type: "ollama"})
					}
					m.connectType, m.connectValues = preset.kind, append([]string(nil), preset.values...)
					m.connectStep = 1
					m.input.SetValue("")
					m.setConnectPrompt()
					return m, nil
				}
				if m.connectStep <= 4 {
					value := strings.TrimSpace(m.input.Value())
					if value != "" {
						m.connectValues[m.connectStep-1] = value
					}
					m.connectStep++
					m.input.SetValue("")
					if m.connectStep <= 4 {
						m.setConnectPrompt()
						return m, nil
					}
					m.status = "Saving provider…"
					return m, connectCmd(m.connect, ConnectionInput{Name: m.connectValues[0], Type: m.connectType, BaseURL: m.connectValues[1], APIKeyEnv: m.connectValues[2], ChatModel: m.connectValues[3]})
				}
			}
			if strings.TrimSpace(m.input.Value()) == "" {
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
			case "/models":
				return m.openModels()
			case "/connect":
				return m.beginConnect(), nil
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
				m.lines = append(m.lines, m.renderAssistantMessage("Commands\n/clear — clear the visible pane\n/reset — clear pane and model history\n/compact — shrink older conversation context\n/help — show this help\n/confirm — apply a reviewed change plan\n/cancel — discard a reviewed change plan\n\nKeys: Enter send · Shift+Enter newline · Esc cancel · Ctrl+C quit"))
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
			return m, m.startSubmit(input)
		}
	case workerMsg:
		if msg.requestID != m.requestID {
			return m, nil
		}
		if !msg.done && msg.text == "" {
			m.status = msg.status
			return m, waitForWorker(m.events)
		}
		if !msg.done {
			if !m.streaming {
				m.lines = append(m.lines, m.renderAssistantMessage(""))
				m.streaming = true
				m.streamText = ""
			}
			m.streamText += msg.text
			m.lines[len(m.lines)-1] = m.renderAssistantMessage(RenderMarkdown(m.streamText))
			m.refreshOutput()
			return m, waitForWorker(m.events)
		}
		m.busy = false
		m.cancel = nil
		m.status = "Ready"
		if msg.err != nil {
			m.lines = append(m.lines, m.renderErrorMessage(msg.err.Error()))
		} else {
			entry := m.renderAssistantMessage(RenderMarkdown(msg.text))
			if m.streaming {
				m.lines[len(m.lines)-1] = entry
			} else {
				m.lines = append(m.lines, entry)
			}
		}
		m.reviewing = msg.err == nil && m.pendingActions != nil && m.pendingActions()
		if m.reviewing {
			m.status = "Apply this plan? Press Y or Enter to approve · N or Esc to cancel"
		}
		m.streaming = false
		m.streamText = ""
		m.refreshOutput()
		return m, nil
	case modelPickerMsg:
		m.pickerLoading = false
		if msg.err != nil {
			m.closeOverlay()
			m.lines = append(m.lines, m.renderErrorMessage(msg.err.Error()))
			m.refreshOutput()
			return m, nil
		}
		m.pickerOptions, m.pickerIndex, m.status = msg.options, 0, "Choose a model"
		return m, nil
	case modelSelectedMsg:
		m.closeOverlay()
		if msg.err != nil {
			m.lines = append(m.lines, m.renderErrorMessage(msg.err.Error()))
		} else {
			m.status = msg.text
		}
		m.refreshOutput()
		return m, nil
	case providerConnectedMsg:
		m.closeOverlay()
		if msg.err != nil {
			m.lines = append(m.lines, m.renderErrorMessage(msg.err.Error()))
		} else {
			m.status = msg.text
		}
		m.refreshOutput()
		return m, nil
	case subscriptionMsg:
		m.closeOverlay()
		if msg.err != nil {
			m.lines = append(m.lines, m.renderErrorMessage(msg.err.Error()))
		} else {
			m.lines = append(m.lines, m.renderAssistantMessage("Open this URL in any browser, sign in, then approve Athena:\n\n"+msg.url+"\n\nAthena will switch to your ChatGPT subscription after approval."))
			m.status = "Waiting for browser approval"
		}
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
	if _, ok := msg.(tea.KeyPressMsg); ok {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

func (m *bubbleModel) startSubmit(input string) tea.Cmd {
	m.input.SetValue("")
	m.busy = true
	m.status = "Starting request…"
	m.followOutput = true
	m.lines = append(m.lines, m.renderUserMessage(input))
	m.refreshOutput()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.requestID++
	id := m.requestID
	events := make(chan workerMsg, 64)
	m.events = events
	go func() {
		emit := func(message workerMsg) {
			select {
			case events <- message:
			default:
			}
		}
		text, err := m.submit(ctx, input, func(status string) { emit(workerMsg{requestID: id, status: status}) }, func(token string) { emit(workerMsg{requestID: id, text: token}) })
		emit(workerMsg{requestID: id, text: text, err: err, done: true})
		close(events)
	}()
	return tea.Batch(waitForWorker(events), m.spinner.Tick)
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

func (m bubbleModel) openModels() (bubbleModel, tea.Cmd) {
	m.picker, m.pickerLoading, m.pickerIndex = true, true, 0
	m.commandMenu = false
	m.input.SetValue("")
	m.input.Placeholder = "Filter by provider or model…"
	m.status = "Loading models…"
	return m, func() tea.Msg {
		options, err := m.models(context.Background())
		return modelPickerMsg{options: options, err: err}
	}
}

func (m bubbleModel) beginConnect() bubbleModel {
	m.picker, m.pickerLoading, m.connecting, m.connectStep, m.pickerIndex = false, false, true, 0, 0
	m.commandMenu = false
	m.input.SetValue("")
	m.status = "Choose provider type"
	return m
}

func (m *bubbleModel) closeOverlay() {
	m.picker, m.pickerLoading, m.connecting, m.connectStep, m.pickerIndex = false, false, false, 0, 0
	m.input.SetValue("")
	m.input.Placeholder = "Ask Athena…"
	if !m.busy {
		m.status = "Ready"
	}
}

func (m *bubbleModel) setConnectPrompt() {
	labels := []string{"Provider name", "Base URL (include /v1)", "API-key environment variable", "Default chat model"}
	m.input.Placeholder = labels[m.connectStep-1]
	m.status = "Connect provider — " + labels[m.connectStep-1]
}

func selectModelCmd(selectModel SelectModelFunc, option ModelOption) tea.Cmd {
	return func() tea.Msg {
		text, err := selectModel(context.Background(), option)
		return modelSelectedMsg{text: text, err: err}
	}
}

func connectCmd(connect ConnectFunc, input ConnectionInput) tea.Cmd {
	return func() tea.Msg { text, err := connect(input); return providerConnectedMsg{text: text, err: err} }
}

type subscriptionMsg struct {
	url string
	err error
}

func subscriptionCmd(subscription SubscriptionFunc) tea.Cmd {
	return func() tea.Msg {
		url, err := subscription(context.Background())
		return subscriptionMsg{url: url, err: err}
	}
}

func (m *bubbleModel) refreshOutput() {
	rendered := make([]string, 0, len(m.lines))
	for _, line := range m.lines {
		rendered = append(rendered, renderChatMessage(line.header, line.text, line.contentStyle, line.railStyle, m.messageWidth()))
	}
	offset := m.output.YOffset()
	m.output.SetContent(strings.Join(rendered, "\n\n"))
	if m.followOutput {
		m.output.GotoBottom()
	} else {
		m.output.SetYOffset(offset)
	}
}

// Chat entries use a restrained rail instead of full boxes. It gives each
// streamed response a stable visual boundary without consuming much terminal
// space or making long answers feel like a stack of UI panels.
func (m bubbleModel) renderUserMessage(text string) chatMessage {
	return chatMessage{header: accent.Render("❯") + muted.Render(" You"), text: text, contentStyle: userMessage, railStyle: accent}
}

func (m bubbleModel) renderAssistantMessage(text string) chatMessage {
	return chatMessage{header: accent.Render("✦ Athena"), text: text, contentStyle: assistantMessage, railStyle: accent}
}

func (m bubbleModel) renderErrorMessage(text string) chatMessage {
	return chatMessage{header: errorMessage.Render("! Request failed"), text: text, contentStyle: errorMessage, railStyle: errorMessage}
}

func renderChatMessage(header, text string, contentStyle, railStyle lipgloss.Style, width int) string {
	text = lipgloss.Wrap(text, max(1, width-2), "")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = railStyle.Render("│ ") + contentStyle.Render(line)
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func (m bubbleModel) messageWidth() int {
	return max(20, m.output.Width())
}

func (m bubbleModel) composerWidth() int {
	return max(18, m.width-4)
}

func (m bubbleModel) composerView() string {
	width := m.composerWidth()
	return panel.Width(width).Render(m.input.View())
}

func (m bubbleModel) approvalView(width int) string {
	if !m.reviewing {
		return ""
	}
	return panel.Width(width).Render(accent.Render("Apply planned changes?") + muted.Render("  [Y] Yes · [N] No"))
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
		suggestions = m.commandPaletteView(width, matches) + "\n"
	}
	composer := m.composerView()
	if m.picker || m.connecting {
		composer = m.overlayView(width)
		suggestions = ""
	}
	approval := m.approvalView(width)
	if approval != "" {
		approval += "\n"
	}
	content := header + "\n" + muted.Render(strings.Repeat("─", width)) + "\n\n" + body + "\n\n" + suggestions + approval + composer + "\n" + status + "\n" + muted.Render("Enter send · Shift+Enter newline · PgUp/PgDn or empty-input arrows scroll · Esc cancel")
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "Athena"
	// Mouse tracking prevents terminal text selection. Keep it disabled so chat
	// messages can be selected and copied with the terminal's normal controls.
	v.MouseMode = tea.MouseModeNone
	return v
}

func (m bubbleModel) commandPaletteView(width int, matches []commandSpec) string {
	limit := min(6, len(matches))
	start := 0
	selected := m.commandIndex % len(matches)
	if selected >= limit {
		start = selected - limit + 1
	}
	var lines []string
	for i := start; i < start+limit && i < len(matches); i++ {
		item := matches[i]
		prefix := "  "
		if i == selected {
			prefix = accent.Render("› ")
		}
		lines = append(lines, prefix+accent.Render(item.name)+muted.Render("  "+item.description))
	}
	footer := muted.Render("↑↓ move · Tab complete · Enter choose · Esc close")
	return panel.Width(width).Render(accent.Render("Commands") + muted.Render("  type to filter") + "\n" + strings.Join(lines, "\n") + "\n" + footer)
}

func (m bubbleModel) overlayView(width int) string {
	if m.picker {
		if m.pickerLoading {
			return panel.Width(width).Render(accent.Render("Models") + "\n" + muted.Render("Loading available models…"))
		}
		matches := m.filteredModels()
		selectedIndex := min(m.pickerIndex, len(matches))
		var lines []string
		for i, option := range matches {
			prefix := "  "
			if i == selectedIndex {
				prefix = accent.Render("› ")
			}
			badge := muted.Render("  " + option.ProviderName)
			if option.Current {
				badge += accent.Render("  ACTIVE")
			}
			lines = append(lines, prefix+assistantMessage.Render(option.Model)+badge)
		}
		if len(matches) == 0 {
			lines = append(lines, muted.Render("  No matching models"))
		}
		marker := "  "
		if selectedIndex == len(matches) {
			marker = accent.Render("› ")
		}
		lines = append(lines, marker+accent.Render("+ Connect a provider"))
		lines = append(lines, muted.Render("↑↓ move · Enter select · type to filter · Esc close"))
		return panel.Width(width).Render(accent.Render("Models") + muted.Render("  /models") + "\n" + m.input.View() + "\n" + muted.Render(strings.Repeat("─", max(12, width-4))) + "\n" + strings.Join(lines, "\n"))
	}
	if m.connectStep == 0 {
		choices := connectChoices()
		var lines []string
		for i, choice := range choices {
			marker := "  "
			if i == m.pickerIndex {
				marker = accent.Render("› ")
			}
			lines = append(lines, marker+choice.label+muted.Render("  "+choice.detail))
		}
		lines = append(lines, muted.Render("↑↓ move · Enter select · Esc close"))
		return panel.Width(width).Render(accent.Render("Connect a provider") + "\n" + strings.Join(lines, "\n"))
	}
	labels := []string{"Provider name", "Base URL", "API-key environment variable", "Default chat model"}
	defaultValue := m.connectValues[m.connectStep-1]
	return panel.Width(width).Render(accent.Render("Connect a provider") + muted.Render("  "+labels[m.connectStep-1]) + "\n" + m.input.View() + "\n" + muted.Render("Enter to continue · leave blank to use: "+defaultValue+" · Esc close"))
	return ""
}

type connectChoice struct {
	label, detail, kind string
	values              []string
}

func connectChoices() []connectChoice {
	return []connectChoice{
		{label: "ChatGPT Plus/Pro", detail: "browser sign-in", kind: "openai_subscription"},
		{label: "OpenAI", detail: "API key", kind: "openai", values: []string{"OpenAI", "https://api.openai.com/v1", "OPENAI_API_KEY", "gpt-5.2"}},
		{label: "Anthropic", detail: "API key", kind: "anthropic", values: []string{"Anthropic", "https://api.anthropic.com/v1", "ANTHROPIC_API_KEY", "claude-sonnet-4-5"}},
		{label: "xAI / Grok", detail: "API key", kind: "openai_compatible", values: []string{"xAI", "https://api.x.ai/v1", "XAI_API_KEY", "grok-4"}},
		{label: "OpenRouter", detail: "API key", kind: "openai_compatible", values: []string{"OpenRouter", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", "openai/gpt-5.2"}},
		{label: "Restore built-in Ollama", detail: "local defaults", kind: "ollama"},
		{label: "Custom local server", detail: "OpenAI-compatible", kind: "openai_compatible", values: []string{"", "http://localhost:1234/v1", "", ""}},
	}
}

func (m bubbleModel) filteredModels() []ModelOption {
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if query == "" {
		return m.pickerOptions
	}
	filtered := make([]ModelOption, 0, len(m.pickerOptions))
	for _, option := range m.pickerOptions {
		if strings.Contains(strings.ToLower(option.Model), query) || strings.Contains(strings.ToLower(option.ProviderName), query) {
			filtered = append(filtered, option)
		}
	}
	return filtered
}
