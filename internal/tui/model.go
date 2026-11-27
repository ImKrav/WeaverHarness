package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/ImKrav/weaverharness/internal/gitx"
	"github.com/ImKrav/weaverharness/internal/provider"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// OrchestratorEvent wraps a normalized event from the orchestrator stream.
type OrchestratorEvent struct{ provider.Event }

// OrchestratorExited notifies the TUI that the orchestrator process terminated.
type OrchestratorExited struct{ Err error }

// AgentsChanged updates the read-only subagent panel.
type AgentsChanged struct{ Agents []engine.AgentInfo }

// ProgressChanged updates the SDD phase and plan tasks summary.
type ProgressChanged struct{ Progress engine.RunProgress }

// RefViolations notifies the chat that the ref guard detected or restored ref mutations.
type RefViolations struct{ Items []gitx.Violation }

// ApprovalAnswer carries the user's approval decision and optional rejection comment.
type ApprovalAnswer struct {
	Approved bool
	Comment  string
}

// ApprovalAsked delivers a pending approval prompt from engine to the TUI.
type ApprovalAsked struct {
	Req   engine.ApprovalRequest
	Reply chan<- ApprovalAnswer
}

// AgentDetail supplies full read-only inspection content for a selected agent.
type AgentDetail struct {
	ID           string
	Receipt      string
	Verification string
	LogTail      string
}

type modalState struct {
	title   string
	agentID string
	vp      viewport.Model
	raw     string
}

type modalLoadedMsg struct {
	title   string
	agentID string
	content string
	err     error
}

type modalTickMsg struct{ agentID string }

type agentCancelledMsg struct {
	agentID string
	err     error
}

// Options configures a TUI Model instance.
type Options struct {
	Title            string
	OrchestratorName string
	Send             func(string) error
	Detail           func(agentID string) AgentDetail
	Preview          func(req engine.ApprovalRequest) (string, error)
	CancelAgent      func(agentID string) error
	OnChatEntry      func(role, text string)
	InitialAgents    []engine.AgentInfo
	InitialProgress  engine.RunProgress
	InitialChat      []string
}

// Model is the Bubble Tea root model for WeaverHarness.
type Model struct {
	title         string
	orchLabel     string
	sendFn        func(string) error
	detailFn      func(agentID string) AgentDetail
	previewFn     func(req engine.ApprovalRequest) (string, error)
	cancelFn      func(agentID string) error
	onChatEntryFn func(role, text string)

	width  int
	height int

	viewport    viewport.Model
	input       textarea.Model
	spinner     spinner.Model
	thinking    bool
	chatLines   []string
	unreadBelow int

	progress      engine.RunProgress
	agents        []engine.AgentInfo
	selected      int
	panelOffset   int
	panelFocused  bool
	confirmCancel string
	modal         *modalState

	pendingApproval *ApprovalAsked
	rejecting       bool
	confirmQuit     bool
}

// New constructs a Model with sensible default dimensions for both live TTY and headless tests.
func New(title string, send func(string) error, detail func(agentID string) AgentDetail) Model {
	return NewWithOptions(Options{
		Title:  title,
		Send:   send,
		Detail: detail,
	})
}

// NewWithOptions constructs a Model configured with initial state, labels, and callbacks.
func NewWithOptions(o Options) Model {
	ta := textarea.New()
	ta.Placeholder = "Escribe un mensaje al orquestador..."
	ta.Prompt = "› "
	ta.ShowLineNumbers = false
	ta.SetHeight(2)
	ta.Focus()

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = stylePrompt

	orchLabel := strings.TrimSpace(o.OrchestratorName)
	if orchLabel == "" {
		orchLabel = "orquestador"
	}

	vp := viewport.New(66, 19)
	vp.MouseWheelEnabled = true
	m := Model{
		title:         o.Title,
		orchLabel:     orchLabel,
		sendFn:        o.Send,
		detailFn:      o.Detail,
		previewFn:     o.Preview,
		cancelFn:      o.CancelAgent,
		onChatEntryFn: o.OnChatEntry,
		width:         100,
		height:        28,
		viewport:      vp,
		input:         ta,
		spinner:       sp,
		progress:      o.InitialProgress,
		agents:        append([]engine.AgentInfo(nil), o.InitialAgents...),
		chatLines:     append([]string(nil), o.InitialChat...),
	}
	m.resize()
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return textarea.Blink
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil

	case spinner.TickMsg:
		if !m.thinking {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.MouseMsg:
		if m.modal != nil {
			var cmd tea.Cmd
			m.modal.vp, cmd = m.modal.vp.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if m.viewport.AtBottom() {
			m.unreadBelow = 0
		}
		return m, cmd

	case modalLoadedMsg:
		if msg.err != nil {
			errText := "error al cargar detalle: " + msg.err.Error()
			m.appendChat(styleError.Render("✗ " + errText))
			m.recordChat("error", errText)
			return m, nil
		}
		_, innerW, vpH := m.modalDims()
		wrapped := wrapChatLines(strings.Split(msg.content, "\n"), innerW)
		if m.modal != nil && msg.agentID != "" && m.modal.agentID == msg.agentID {
			wasAtBottom := m.modal.vp.AtBottom()
			prevOffset := m.modal.vp.YOffset
			m.modal.title = msg.title
			m.modal.raw = msg.content
			m.modal.vp.Width = innerW
			m.modal.vp.Height = vpH
			m.modal.vp.SetContent(wrapped)
			if wasAtBottom {
				m.modal.vp.GotoBottom()
			} else {
				m.modal.vp.SetYOffset(prevOffset)
			}
		} else {
			vp := viewport.New(innerW, vpH)
			vp.MouseWheelEnabled = true
			vp.SetContent(wrapped)
			m.modal = &modalState{
				title:   msg.title,
				agentID: msg.agentID,
				vp:      vp,
				raw:     msg.content,
			}
		}
		if msg.agentID != "" && m.isAgentActive(msg.agentID) {
			return m, scheduleModalTick(msg.agentID)
		}
		return m, nil

	case modalTickMsg:
		if m.modal == nil || m.modal.agentID == "" || m.modal.agentID != msg.agentID || !m.isAgentActive(msg.agentID) || m.detailFn == nil {
			return m, nil
		}
		return m, loadAgentDetailCmd(m.detailFn, msg.agentID)

	case agentCancelledMsg:
		if msg.err != nil {
			errText := fmt.Sprintf("error al cancelar %s: %v", msg.agentID, msg.err)
			m.appendChat(styleError.Render("✗ " + errText))
			m.recordChat("error", errText)
		} else {
			text := fmt.Sprintf("Agente %s cancelado.", msg.agentID)
			m.appendChat(styleWarn.Render("✕ " + text))
			m.recordChat("system", text)
		}
		return m, nil

	case OrchestratorEvent:
		m.handleOrchestratorEvent(msg.Event)
		if msg.Event.Kind == provider.EventTurnEnd {
			m.thinking = false
		}
		return m, nil

	case OrchestratorExited:
		m.thinking = false
		if msg.Err != nil {
			text := fmt.Sprintf("Orquestador terminó (%v). Usa 'weaver resume' para retomar.", msg.Err)
			m.appendChat(styleError.Render("✗ " + text))
			m.recordChat("error", text)
		} else {
			text := "Orquestador terminó. Usa 'weaver resume' para retomar."
			m.appendChat(styleWarn.Render(text))
			m.recordChat("system", text)
		}
		return m, nil

	case AgentsChanged:
		m.agents = append([]engine.AgentInfo(nil), msg.Agents...)
		if m.selected >= len(m.agents) {
			m.selected = max(0, len(m.agents)-1)
		}
		m.adjustPanelOffset()
		return m, nil

	case ProgressChanged:
		m.progress = msg.Progress
		m.adjustPanelOffset()
		return m, nil

	case RefViolations:
		for _, v := range msg.Items {
			note := fmt.Sprintf("⚠ Guardia de refs: %s %s", v.Kind, v.Ref)
			if v.Restored {
				note += " (restaurada)"
			}
			m.appendChat(styleWarn.Render(note))
			m.recordChat("system", note)
		}
		return m, nil

	case ApprovalAsked:
		cp := msg
		m.pendingApproval = &cp
		m.rejecting = false
		promptLine := fmt.Sprintf("Aprobación solicitada: %s (%s)", msg.Req.Kind, msg.Req.Path)
		m.appendChat(stylePrompt.Render("⚑ " + promptLine))
		m.recordChat("system", promptLine)
		if msg.Req.Summary != "" {
			summaryLine := "  " + msg.Req.Summary
			m.appendChat(summaryLine)
			m.recordChat("system", summaryLine)
		}
		if msg.Req.Kind == "plan" && msg.Req.Plan != nil {
			for _, t := range msg.Req.Plan.Tasks {
				line := fmt.Sprintf("  · %s [%s] %s", t.ID, t.Role, t.Title)
				if len(t.DependsOn) > 0 {
					line += fmt.Sprintf(" (deps: %s)", strings.Join(t.DependsOn, ","))
				}
				if len(t.Checks) > 0 {
					line += fmt.Sprintf(" | checks: %s", strings.Join(t.Checks, "; "))
				}
				m.appendChat(line)
				m.recordChat("system", line)
			}
			if len(msg.Req.Plan.GlobalChecks) > 0 {
				gcLine := "  · global_checks: " + strings.Join(msg.Req.Plan.GlobalChecks, "; ")
				m.appendChat(gcLine)
				m.recordChat("system", gcLine)
			}
		}
		return m, nil

	case approvalCancelledMsg:
		if m.pendingApproval != nil && m.pendingApproval.Reply == msg.reply {
			m.pendingApproval = nil
			m.rejecting = false
			m.appendChat(styleDim.Render("⚑ Aprobación cancelada."))
			m.recordChat("system", "Aprobación cancelada.")
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.confirmQuit = true
		return m, nil
	}

	if m.confirmQuit {
		switch strings.ToLower(msg.String()) {
		case "s", "y":
			return m, tea.Quit
		case "n", "esc", "enter":
			m.confirmQuit = false
			return m, nil
		}
		return m, nil
	}

	if m.confirmCancel != "" {
		switch strings.ToLower(msg.String()) {
		case "s", "y":
			id := m.confirmCancel
			m.confirmCancel = ""
			if m.cancelFn == nil {
				m.appendChat(styleDim.Render("Cancelación no disponible."))
				return m, nil
			}
			return m, cancelAgentCmd(m.cancelFn, id)
		case "n", "esc", "enter":
			m.confirmCancel = ""
			return m, nil
		}
		return m, nil
	}

	if m.modal != nil {
		switch {
		case msg.Type == tea.KeyEsc, strings.ToLower(msg.String()) == "q":
			m.modal = nil
			m.panelFocused = false
		case msg.Type == tea.KeyUp, msg.String() == "k":
			m.modal.vp.ScrollUp(1)
		case msg.Type == tea.KeyDown, msg.String() == "j":
			m.modal.vp.ScrollDown(1)
		case msg.Type == tea.KeyPgUp, msg.String() == "b":
			m.modal.vp.PageUp()
		case msg.Type == tea.KeyPgDown, msg.String() == "f", msg.String() == " ":
			m.modal.vp.PageDown()
		case msg.Type == tea.KeyHome, msg.String() == "g":
			m.modal.vp.GotoTop()
		case msg.Type == tea.KeyEnd, msg.String() == "G":
			m.modal.vp.GotoBottom()
		}
		return m, nil
	}

	if msg.Type == tea.KeyCtrlJ || (msg.Alt && msg.Type == tea.KeyEnter) {
		m.panelFocused = false
		m.input.InsertRune('\n')
		return m, nil
	}

	if m.pendingApproval != nil && !m.rejecting && strings.TrimSpace(m.input.Value()) == "" {
		switch strings.ToLower(msg.String()) {
		case "a":
			ans := ApprovalAnswer{Approved: true}
			m.pendingApproval.Reply <- ans
			text := fmt.Sprintf("Aprobado: %s", m.pendingApproval.Req.Kind)
			m.appendChat(styleOK.Render("✓ " + text))
			m.recordChat("system", text)
			m.pendingApproval = nil
			return m, nil
		case "r":
			m.rejecting = true
			m.panelFocused = false
			m.input.Reset()
			m.input.Placeholder = "Escribe el motivo del rechazo y pulsa Enter..."
			m.input.Focus()
			return m, nil
		case "v":
			if m.previewFn == nil {
				m.appendChat(styleDim.Render("Previsualización no disponible."))
				return m, nil
			}
			return m, loadPreviewCmd(m.previewFn, m.pendingApproval.Req)
		}
	}

	if m.panelFocused && !m.rejecting && len(m.agents) > 0 && strings.ToLower(msg.String()) == "x" {
		ag := m.agents[m.selected]
		if ag.Status == "queued" || ag.Status == "running" {
			m.confirmCancel = ag.ID
			return m, nil
		}
		m.appendChat(styleDim.Render(fmt.Sprintf("El agente %s ya no está activo (%s).", ag.ID, ag.Status)))
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		if m.rejecting {
			m.rejecting = false
			m.input.Reset()
			m.input.Placeholder = "Escribe un mensaje al orquestador..."
			return m, nil
		}
		m.panelFocused = false
		return m, nil

	case tea.KeyTab:
		if !m.rejecting && len(m.agents) > 0 {
			m.panelFocused = !m.panelFocused
			m.adjustPanelOffset()
		}
		return m, nil

	case tea.KeyUp:
		if !m.rejecting && len(m.agents) > 0 && (m.panelFocused || strings.TrimSpace(m.input.Value()) == "") {
			m.panelFocused = true
			if m.selected > 0 {
				m.selected--
				m.adjustPanelOffset()
			}
			return m, nil
		}

	case tea.KeyDown:
		if !m.rejecting && len(m.agents) > 0 && (m.panelFocused || strings.TrimSpace(m.input.Value()) == "") {
			m.panelFocused = true
			if m.selected < len(m.agents)-1 {
				m.selected++
				m.adjustPanelOffset()
			}
			return m, nil
		}

	case tea.KeyPgUp:
		m.viewport.PageUp()
		return m, nil

	case tea.KeyPgDown:
		m.viewport.PageDown()
		if m.viewport.AtBottom() {
			m.unreadBelow = 0
		}
		return m, nil

	case tea.KeyHome:
		m.viewport.GotoTop()
		return m, nil

	case tea.KeyEnd:
		m.viewport.GotoBottom()
		m.unreadBelow = 0
		return m, nil

	case tea.KeyEnter:
		if m.rejecting && m.pendingApproval != nil {
			comment := strings.TrimSpace(m.input.Value())
			m.pendingApproval.Reply <- ApprovalAnswer{Approved: false, Comment: comment}
			text := fmt.Sprintf("Rechazado: %s (%s)", m.pendingApproval.Req.Kind, comment)
			m.appendChat(styleError.Render("✗ " + text))
			m.recordChat("system", text)
			m.pendingApproval = nil
			m.rejecting = false
			m.input.Reset()
			m.input.Placeholder = "Escribe un mensaje al orquestador..."
			return m, nil
		}
		if m.panelFocused && len(m.agents) > 0 && m.detailFn != nil {
			agentID := m.agents[m.selected].ID
			return m, loadAgentDetailCmd(m.detailFn, agentID)
		}
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.Reset()
		m.panelFocused = false
		m.appendChat("tú › " + text)
		m.recordChat("user", text)
		m.viewport.GotoBottom()
		m.unreadBelow = 0
		if m.sendFn != nil {
			if err := m.sendFn(text); err != nil {
				errText := "error al enviar: " + err.Error()
				m.appendChat(styleError.Render("✗ " + errText))
				m.recordChat("error", errText)
				return m, nil
			}
		}
		m.thinking = true
		return m, m.spinner.Tick
	}

	// Typing returns focus to chat textarea.
	m.panelFocused = false
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) adjustPanelOffset() {
	total := len(m.agents)
	if total == 0 {
		m.selected = 0
		m.panelOffset = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= total {
		m.selected = total - 1
	}

	_, _, _, bodyH := m.layout()
	avail := panelAgentAvailLines(bodyH, len(m.progress.Tasks))
	if total*2 <= avail {
		m.panelOffset = 0
		return
	}

	m.panelOffset = max(0, min(m.panelOffset, total-1))
	if m.selected < m.panelOffset {
		m.panelOffset = m.selected
		return
	}
	for {
		_, end, _, _ := agentWindowRange(total, m.panelOffset, avail)
		if m.selected < end || m.panelOffset >= m.selected {
			break
		}
		m.panelOffset++
	}
}

func panelAgentAvailLines(bodyH, numTasks int) int {
	used := 1 // "Agentes" header
	if numTasks > 0 {
		used += 2 + numTasks // "Tareas" + tasks + blank line
	}
	return max(3, bodyH-used)
}

func agentWindowRange(total, offset, avail int) (start, end int, hasUp, hasDown bool) {
	if total <= 0 {
		return 0, 0, false, false
	}
	if total*2 <= avail {
		return 0, total, false, false
	}
	offset = max(0, min(offset, total-1))
	hasUp = offset > 0
	rem := avail
	if hasUp {
		rem--
	}
	if (total-offset)*2 <= rem {
		return offset, total, hasUp, false
	}
	rem-- // reserve 1 line for bottom scroll indicator
	count := max(1, rem/2)
	end = offset + count
	if end >= total {
		return offset, total, hasUp, false
	}
	return offset, end, hasUp, true
}

func (m Model) isAgentActive(agentID string) bool {
	for _, ag := range m.agents {
		if ag.ID == agentID {
			return ag.Status == "queued" || ag.Status == "running"
		}
	}
	return false
}

func loadAgentDetailCmd(fn func(string) AgentDetail, agentID string) tea.Cmd {
	return func() tea.Msg {
		d := fn(agentID)
		return modalLoadedMsg{
			title:   fmt.Sprintf("Detalle del agente %s", agentID),
			agentID: agentID,
			content: formatAgentDetail(d),
		}
	}
}

func loadPreviewCmd(fn func(engine.ApprovalRequest) (string, error), req engine.ApprovalRequest) tea.Cmd {
	return func() tea.Msg {
		content, err := fn(req)
		title := fmt.Sprintf("Vista previa: %s", req.Kind)
		if req.Path != "" {
			title = fmt.Sprintf("Vista previa: %s (%s)", req.Kind, req.Path)
		}
		return modalLoadedMsg{
			title:   title,
			content: content,
			err:     err,
		}
	}
}

func cancelAgentCmd(fn func(string) error, agentID string) tea.Cmd {
	return func() tea.Msg {
		return agentCancelledMsg{agentID: agentID, err: fn(agentID)}
	}
}

func scheduleModalTick(agentID string) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return modalTickMsg{agentID: agentID}
	})
}

func formatAgentDetail(d AgentDetail) string {
	var b strings.Builder
	if d.Receipt != "" {
		b.WriteString(styleHeader.Render("Recibo:"))
		b.WriteByte('\n')
		b.WriteString(d.Receipt)
		b.WriteString("\n\n")
	}
	if d.Verification != "" {
		b.WriteString(styleHeader.Render("Verificación:"))
		b.WriteByte('\n')
		b.WriteString(d.Verification)
		b.WriteString("\n\n")
	}
	if d.LogTail != "" {
		b.WriteString(styleHeader.Render("Log:"))
		b.WriteByte('\n')
		b.WriteString(d.LogTail)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return "(sin datos todavía)"
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) handleOrchestratorEvent(ev provider.Event) {
	switch ev.Kind {
	case provider.EventText:
		if ev.Text != "" {
			m.appendChat(m.orchLabel + " › " + ev.Text)
			m.recordChat("orchestrator", ev.Text)
		}
	case provider.EventTool:
		if ev.Tool != "" {
			m.appendChat(styleDim.Render("· " + ev.Tool))
			m.recordChat("tool", ev.Tool)
		}
	case provider.EventError:
		if ev.Text != "" {
			m.appendChat(styleError.Render("✗ " + ev.Text))
			m.recordChat("error", ev.Text)
		}
	}
}

func (m *Model) recordChat(role, text string) {
	if m.onChatEntryFn != nil && text != "" {
		m.onChatEntryFn(role, text)
	}
}

func (m *Model) appendChat(line string) {
	wasAtBottom := m.viewport.AtBottom()
	m.chatLines = append(m.chatLines, line)
	m.viewport.SetContent(wrapChatLines(m.chatLines, m.viewport.Width))
	if wasAtBottom {
		m.viewport.GotoBottom()
		m.unreadBelow = 0
	} else {
		m.unreadBelow++
	}
}

func wrapChatLines(lines []string, width int) string {
	if width <= 0 || len(lines) == 0 {
		return strings.Join(lines, "\n")
	}
	wrapped := make([]string, 0, len(lines))
	for _, l := range lines {
		w := ansi.Wordwrap(l, width, "")
		for _, seg := range strings.Split(w, "\n") {
			if ansi.StringWidth(seg) > width {
				seg = ansi.Hardwrap(seg, width, true)
			}
			wrapped = append(wrapped, seg)
		}
	}
	return strings.Join(wrapped, "\n")
}

func (m Model) layout() (totalW, panelW, chatW, bodyH int) {
	totalW = max(40, m.width)
	totalH := max(14, m.height)

	panelW = min(44, max(26, totalW/3))
	if totalW-panelW-3 < 18 {
		panelW = max(16, totalW-21)
	}
	chatW = max(20, totalW-panelW-3)
	bodyH = max(6, totalH-8)
	return totalW, panelW, chatW, bodyH
}

func (m Model) modalDims() (boxW, innerW, vpH int) {
	totalW := max(40, m.width)
	totalH := max(14, m.height)
	boxW = max(36, totalW-4)
	innerW = max(20, boxW-4)
	vpH = max(5, totalH-7)
	return boxW, innerW, vpH
}

func (m *Model) resize() {
	totalW, _, chatW, bodyH := m.layout()

	wasAtBottom := m.viewport.AtBottom()
	m.viewport.Width = chatW
	m.viewport.Height = bodyH
	m.input.SetWidth(totalW - 4)
	m.viewport.SetContent(wrapChatLines(m.chatLines, chatW))
	if wasAtBottom {
		m.viewport.GotoBottom()
		m.unreadBelow = 0
	}

	m.adjustPanelOffset()

	if m.modal != nil {
		_, innerW, vpH := m.modalDims()
		mWasAtBottom := m.modal.vp.AtBottom()
		mPrevOffset := m.modal.vp.YOffset
		m.modal.vp.Width = innerW
		m.modal.vp.Height = vpH
		m.modal.vp.SetContent(wrapChatLines(strings.Split(m.modal.raw, "\n"), innerW))
		if mWasAtBottom {
			m.modal.vp.GotoBottom()
		} else {
			m.modal.vp.SetYOffset(mPrevOffset)
		}
	}
}

var (
	styleHeader = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleError  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	stylePrompt = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	styleBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
)
