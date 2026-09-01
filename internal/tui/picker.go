package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muzzacode/moz/internal/cost"
	"github.com/muzzacode/moz/internal/models"
)

// visibleRows caps how much of the list is drawn at once so a long registry
// cannot push the conversation off screen.
const visibleRows = 10

// nameWidth is the model-name column width. Fixed so the tier and price columns
// line up regardless of how long a profile name is.
const nameWidth = 26

// fit pads or truncates s to exactly n display columns.
func fit(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

type pickerItem struct {
	id        string
	name      string
	tier      string
	price     string
	available bool
	current   bool
}

// picker is a keyboard-driven list overlay.
//
// Choosing a model is a cost decision, so each row carries its tier, price, and
// whether its credential is actually present. Reading that from a static dump
// and then typing an ID from memory is a worse workflow than picking from a list
// that already shows the trade-off.
type picker struct {
	active bool
	items  []pickerItem
	cursor int
	// offset is the first visible row, so the window follows the cursor.
	offset int
	title  string
}

func newModelPicker(reg *models.Registry, router availabilityChecker, currentID string) picker {
	profiles := reg.List()
	items := make([]pickerItem, 0, len(profiles))

	for i := range profiles {
		p := &profiles[i]
		items = append(items, pickerItem{
			id:        p.ID,
			name:      p.Name,
			tier:      p.CostTier,
			price:     cost.Describe(p.ID),
			available: router.Available(p),
			current:   p.ID == currentID,
		})
	}

	pk := picker{active: true, items: items, title: "Select a model"}
	// Start on the model in use, so the list opens where the user already is.
	for i, it := range items {
		if it.current {
			pk.cursor = i
			break
		}
	}
	pk.clampOffset()
	return pk
}

// availabilityChecker is the part of the router the picker needs, kept narrow so
// the picker can be tested without a live registry.
type availabilityChecker interface {
	Available(p *models.Profile) bool
}

func (p *picker) moveBy(delta int) {
	if len(p.items) == 0 {
		return
	}
	p.cursor += delta
	// Clamp rather than wrap: wrapping makes it easy to overshoot past the end
	// and land somewhere unintended.
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(p.items) {
		p.cursor = len(p.items) - 1
	}
	p.clampOffset()
}

// clampOffset keeps the cursor inside the visible window.
func (p *picker) clampOffset() {
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+visibleRows {
		p.offset = p.cursor - visibleRows + 1
	}
	if p.offset < 0 {
		p.offset = 0
	}
}

// selected returns the highlighted item.
func (p *picker) selected() (pickerItem, bool) {
	if !p.active || p.cursor < 0 || p.cursor >= len(p.items) {
		return pickerItem{}, false
	}
	return p.items[p.cursor], true
}

var (
	pickerCursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ECDC4"))
	pickerDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7F8C8D"))
	pickerTierStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F"))
)

func (p *picker) View() string {
	if !p.active {
		return ""
	}

	var b strings.Builder
	b.WriteString(pickerCursorStyle.Render(p.title))
	fmt.Fprintf(&b, "  (%d/%d)\n", p.cursor+1, len(p.items))

	end := p.offset + visibleRows
	if end > len(p.items) {
		end = len(p.items)
	}

	for i := p.offset; i < end; i++ {
		it := p.items[i]

		marker := "  "
		if i == p.cursor {
			marker = "▸ "
		}

		// Names are padded to a fixed width and truncated beyond it, so a long
		// name cannot shunt the price column out of alignment.
		line := fmt.Sprintf("%-*s %-14s %-18s", nameWidth, fit(it.name, nameWidth), it.tier, it.price)
		// An unusable model is shown rather than hidden, so a missing key is
		// visible instead of looking like the model does not exist.
		if !it.available {
			line += " (no key)"
		}
		if it.current {
			line += " (current)"
		}

		switch {
		case i == p.cursor:
			b.WriteString(pickerCursorStyle.Render(marker + line))
		case !it.available:
			b.WriteString(pickerDimStyle.Render(marker + line))
		default:
			b.WriteString(marker + pickerTierStyle.Render("") + line)
		}
		b.WriteString("\n")
	}

	if len(p.items) > visibleRows {
		fmt.Fprintf(&b, pickerDimStyle.Render("  showing %d-%d of %d\n"), p.offset+1, end, len(p.items))
	}
	b.WriteString(pickerDimStyle.Render("  ↑/↓ move · enter select · esc cancel"))
	return b.String()
}
