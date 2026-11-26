package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/ImKrav/weaverharness/internal/engine"
	"github.com/charmbracelet/lipgloss"
)

// View implements tea.Model.
func (m Model) View() string {
	if m.modal != nil {
		return m.renderModal(*m.modal)
	}

	totalW := max(40, m.width)
	panelW := min(34, max(24, totalW/3))
	chatW := max(20, totalW-panelW-3)
	bodyH := max(6, m.height-7)

	header := styleHeader.Render(m.title)

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
		Render(m.renderAgentsPanel(panelW - 2))

	body := lipgloss.JoinHorizontal(lipgloss.Top, chatPane, panelPane)

	var banner string
	switch {
	case m.confirmQuit:
		banner = styleWarn.Render("¿Parar todo? [s/N]")
	case m.pendingApproval != nil && m.rejecting:
		banner = stylePrompt.Render(fmt.Sprintf("⚑ Rechazar %s — escribe comentario y pulsa Enter (Esc cancela)", m.pendingApproval.Req.Kind))
	case m.pendingApproval != nil:
		banner = stylePrompt.Render(fmt.Sprintf("⚑ Aprobación: %s — [a]probar [r]echazar", m.pendingApproval.Req.Kind))
	}

	footer := m.input.View()
	parts := []string{header, body}
	if banner != "" {
		parts = append(parts, banner)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) renderAgentsPanel(width int) string {
	var b strings.Builder
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
		if len([]rune(line1)) > width && width > 4 {
			line1 = string([]rune(line1)[:width-1]) + "…"
		}
		b.WriteString(line1)
		b.WriteByte('\n')

		sub := agentSubtitle(ag)
		line2 := "    " + sub
		if len([]rune(line2)) > width && width > 4 {
			line2 = string([]rune(line2)[:width-1]) + "…"
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
