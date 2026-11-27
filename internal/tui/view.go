package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/ImKrav/weaverharness/internal/run"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// View implements tea.Model.
func (m Model) View() string {
	if m.modal != nil {
		return m.renderModal(*m.modal)
	}

	totalW, panelW, chatW, bodyH := m.layout()

	header := m.renderHeader(totalW)

	chatPane := lipgloss.NewStyle().
		Width(chatW).
		Height(bodyH).
		Render(m.viewport.View())

	panelPane := lipgloss.NewStyle().
		Width(panelW).
		Height(bodyH).
		PaddingLeft(1).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		Render(m.renderSidePanel(panelW - 2))

	body := lipgloss.JoinHorizontal(lipgloss.Top, chatPane, panelPane)

	var banner string
	switch {
	case m.confirmQuit:
		banner = styleWarn.Render("¿Parar todo? [s/N]")
	case m.pendingApproval != nil && m.rejecting:
		banner = stylePrompt.Render(fmt.Sprintf("⚑ Rechazar %s — escribe comentario y pulsa Enter (Esc cancela)", m.pendingApproval.Req.Kind))
	case m.pendingApproval != nil && strings.TrimSpace(m.input.Value()) != "":
		banner = stylePrompt.Render(fmt.Sprintf("⚑ Aprobación: %s — vacía el input para usar [a]/[r]/[v]", m.pendingApproval.Req.Kind))
	case m.pendingApproval != nil:
		banner = stylePrompt.Render(fmt.Sprintf("⚑ Aprobación: %s — [a]probar [r]echazar [v]er", m.pendingApproval.Req.Kind))
	}

	footer := m.input.View()
	helpFooter := m.renderHelpFooter(totalW)

	parts := []string{header, body}
	if banner != "" {
		parts = append(parts, banner)
	}
	parts = append(parts, footer, helpFooter)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) renderHelpFooter(totalW int) string {
	var help string
	switch {
	case m.pendingApproval != nil && m.rejecting:
		help = "Enter enviar rechazo · Esc cancelar"
	case m.pendingApproval != nil:
		help = "[a] aprobar  [r] rechazar  [v] ver detalle  ·  Ctrl+C salir"
	default:
		help = "Tab panel · Ctrl+J nueva línea · Shift+arrastrar copiar · Ctrl+C salir"
	}

	if m.unreadBelow > 0 {
		badge := fmt.Sprintf("↓ %d nuevos", m.unreadBelow)
		line := badge + " · " + help
		if totalW > 4 && ansi.StringWidth(line) > totalW {
			return stylePrompt.Render(ansi.Truncate(line, totalW, "…"))
		}
		return stylePrompt.Render(badge) + styleDim.Render(" · "+help)
	}
	if totalW > 4 && ansi.StringWidth(help) > totalW {
		help = ansi.Truncate(help, totalW, "…")
	}
	return styleDim.Render(help)
}

func (m Model) renderHeader(totalW int) string {
	title := strings.TrimSpace(m.title)
	if title == "" {
		title = "WeaverHarness"
	}

	parts := []string{title}
	if m.progress.RunID != "" {
		parts = append(parts, "run "+m.progress.RunID)
	}
	if m.progress.Phase != "" {
		if m.progress.TasksTotal > 0 {
			parts = append(parts, fmt.Sprintf("%s · %d/%d tareas", m.progress.Phase, m.progress.TasksAccepted, m.progress.TasksTotal))
		} else {
			parts = append(parts, string(m.progress.Phase))
		}
	}
	text := strings.Join(parts, " · ")
	if m.thinking {
		text += " · " + m.spinner.View() + " pensando…"
	}
	if totalW > 4 && ansi.StringWidth(text) > totalW {
		text = ansi.Truncate(text, totalW, "…")
	}
	return styleHeader.Render(text)
}

func (m Model) renderSidePanel(width int) string {
	var b strings.Builder

	if len(m.progress.Tasks) > 0 {
		b.WriteString(styleHeader.Render("Tareas"))
		b.WriteByte('\n')
		for _, t := range m.progress.Tasks {
			line := fmt.Sprintf("%s %s", taskIcon(t.Status), t.ID)
			if t.Title != "" {
				line += " " + t.Title
			}
			if width > 4 && ansi.StringWidth(line) > width {
				line = ansi.Truncate(line, width, "…")
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	b.WriteString(styleHeader.Render("Agentes"))
	b.WriteByte('\n')

	if len(m.agents) == 0 {
		b.WriteString(styleDim.Render("(ninguno)"))
		return b.String()
	}

	for i, ag := range m.agents {
		prefix := "  "
		if m.panelFocused && i == m.selected {
			prefix = "▸ "
		}
		icon := agentIcon(ag)
		line1 := fmt.Sprintf("%s%s %s %s %s/%s", prefix, icon, ag.ID, ag.TaskID, ag.Provider, ag.Model)
		if width > 4 && ansi.StringWidth(line1) > width {
			line1 = ansi.Truncate(line1, width, "…")
		}
		b.WriteString(line1)
		b.WriteByte('\n')

		sub := agentSubtitle(ag)
		line2 := "    " + sub
		if width > 4 && ansi.StringWidth(line2) > width {
			line2 = ansi.Truncate(line2, width, "…")
		}
		b.WriteString(styleDim.Render(line2))
		b.WriteByte('\n')
	}
	return b.String()
}

func (m Model) renderModal(d AgentDetail) string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(fmt.Sprintf("Detalle del agente %s (Esc para cerrar)", d.ID)))
	b.WriteString("\n\n")
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
	return styleBorder.Width(max(40, m.width-4)).Render(b.String())
}

func taskIcon(status run.TaskStatus) string {
	switch status {
	case run.TaskAccepted:
		return "✓"
	case run.TaskRunning:
		return "●"
	case run.TaskReceiptOK:
		return "◐"
	case run.TaskReceiptFailed:
		return "✗"
	default:
		return "○"
	}
}

func agentIcon(ag engine.AgentInfo) string {
	switch ag.Status {
	case "queued", "running":
		return "●"
	case "finished":
		if ag.ReceiptOK != nil && *ag.ReceiptOK && ag.Verified != nil && *ag.Verified {
			return "✓"
		}
		return "✗"
	default:
		return "✗"
	}
}

func agentSubtitle(ag engine.AgentInfo) string {
	switch ag.Status {
	case "queued":
		return "en cola"
	case "running":
		if !ag.Started.IsZero() {
			return fmt.Sprintf("corriendo · %s", time.Since(ag.Started).Truncate(time.Second))
		}
		return "corriendo"
	case "finished":
		if ag.ReceiptOK != nil && *ag.ReceiptOK && ag.Verified != nil && *ag.Verified {
			return "recibo ✓"
		}
		if ag.Err != "" {
			return ag.Err
		}
		return "verificación fallida"
	default:
		if ag.Err != "" {
			return ag.Err
		}
		return ag.Status
	}
}
