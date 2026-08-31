package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muzzacode/moz/internal/models"
)

type stubAvailability struct{ unavailable map[string]bool }

func (s stubAvailability) Available(p *models.Profile) bool { return !s.unavailable[p.ID] }

func pickerRegistry() *models.Registry {
	return &models.Registry{
		Profiles: []models.Profile{
			{ID: "local-coder", Name: "Local Coder", CostTier: "local"},
			{ID: "glm-flash", Name: "GLM 5.3 Flash", CostTier: "cloud-cheap"},
			{ID: "claude-opus-5", Name: "Claude Opus 5", CostTier: "cloud-premium"},
		},
	}
}

func TestPickerOpensOnCurrentModel(t *testing.T) {
	p := newModelPicker(pickerRegistry(), stubAvailability{}, "glm-flash")
	if !p.active {
		t.Fatal("picker should be active")
	}
	sel, ok := p.selected()
	if !ok || sel.id != "glm-flash" {
		t.Fatalf("expected the current model preselected, got %+v", sel)
	}
}

func TestPickerMovesAndClampsAtBothEnds(t *testing.T) {
	p := newModelPicker(pickerRegistry(), stubAvailability{}, "local-coder")

	p.moveBy(1)
	if sel, _ := p.selected(); sel.id != "glm-flash" {
		t.Fatalf("down should advance, got %s", sel.id)
	}

	// Clamping rather than wrapping: overshooting must not land at the far end.
	p.moveBy(100)
	if sel, _ := p.selected(); sel.id != "claude-opus-5" {
		t.Fatalf("expected clamp to the last item, got %s", sel.id)
	}
	p.moveBy(-100)
	if sel, _ := p.selected(); sel.id != "local-coder" {
		t.Fatalf("expected clamp to the first item, got %s", sel.id)
	}
}

// A model with no key is shown, not hidden, so a missing credential looks like a
// missing credential rather than a missing model.
func TestPickerShowsUnavailableModelsWithReason(t *testing.T) {
	p := newModelPicker(pickerRegistry(), stubAvailability{unavailable: map[string]bool{"claude-opus-5": true}}, "local-coder")

	view := p.View()
	if !strings.Contains(view, "Claude Opus 5") {
		t.Fatal("unavailable models must still be listed")
	}
	if !strings.Contains(view, "no key") {
		t.Fatalf("expected a reason for unavailability:\n%s", view)
	}
}

func TestPickerViewShowsTierAndPriceAndCurrent(t *testing.T) {
	p := newModelPicker(pickerRegistry(), stubAvailability{}, "glm-flash")
	view := p.View()

	for _, want := range []string{"local", "cloud-cheap", "cloud-premium", "free", "(current)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	// Price is the point of the list, so a real figure must appear.
	if !strings.Contains(view, "$") {
		t.Fatalf("expected prices in the view:\n%s", view)
	}
}

func TestPickerWindowFollowsCursorForLongLists(t *testing.T) {
	reg := &models.Registry{}
	for i := 0; i < 30; i++ {
		reg.Profiles = append(reg.Profiles, models.Profile{
			ID: string(rune('a'+i%26)) + strings.Repeat("x", i/26), Name: "M", CostTier: "local",
		})
	}
	p := newModelPicker(reg, stubAvailability{}, "")

	p.moveBy(25)
	if p.cursor < p.offset || p.cursor >= p.offset+visibleRows {
		t.Fatalf("cursor %d outside window starting at %d", p.cursor, p.offset)
	}
	p.moveBy(-25)
	if p.cursor < p.offset || p.cursor >= p.offset+visibleRows {
		t.Fatalf("cursor %d outside window starting at %d after moving back", p.cursor, p.offset)
	}
}

func TestPickerInactiveRendersNothing(t *testing.T) {
	var p picker
	if p.View() != "" {
		t.Fatal("an inactive picker must render nothing")
	}
	if _, ok := p.selected(); ok {
		t.Fatal("an inactive picker has no selection")
	}
}

// While the picker is open it must own the keyboard, or navigation keys end up
// typed into the prompt.
func TestPickerKeysDoNotReachTextarea(t *testing.T) {
	m := newTestModel(t)
	m.modelPicker = newModelPicker(pickerRegistry(), stubAvailability{}, "local-coder")

	before := m.textarea.Value()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := asModel(t, updated)

	if got.textarea.Value() != before {
		t.Fatalf("picker keys leaked into the input: %q", got.textarea.Value())
	}
	if sel, _ := got.modelPicker.selected(); sel.id != "glm-flash" {
		t.Fatalf("j should move down, got %+v", sel)
	}
}

func TestPickerEnterSwitchesModelAndLocksMode(t *testing.T) {
	m := newTestModel(t)
	m.registry = pickerRegistry()
	m.mode = "adaptive"
	m.modelPicker = newModelPicker(m.registry, stubAvailability{}, "local-coder")
	m.modelPicker.moveBy(1) // glm-flash

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := asModel(t, updated)

	if got.modelPicker.active {
		t.Fatal("picker should close after selection")
	}
	if got.profile.ID != "glm-flash" {
		t.Fatalf("expected glm-flash, got %s", got.profile.ID)
	}
	// An explicit choice must not be overridden by adaptive routing.
	if got.mode != "manual" {
		t.Fatalf("expected manual mode after an explicit choice, got %q", got.mode)
	}
}

// Selecting a model with no key must warn immediately, not fail later.
func TestPickerWarnsWhenSelectingUnavailableModel(t *testing.T) {
	m := newTestModel(t)
	m.registry = pickerRegistry()
	m.modelPicker = newModelPicker(m.registry, stubAvailability{unavailable: map[string]bool{"claude-opus-5": true}}, "local-coder")
	m.modelPicker.moveBy(2) // claude-opus-5

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := asModel(t, updated)

	if !strings.Contains(systemText(got), "no API key") {
		t.Fatalf("expected a missing-key warning, got %q", systemText(got))
	}
}

func TestPickerEscCancelsWithoutChangingModel(t *testing.T) {
	m := newTestModel(t)
	m.registry = pickerRegistry()
	original := m.profile.ID
	m.modelPicker = newModelPicker(m.registry, stubAvailability{}, original)
	m.modelPicker.moveBy(1)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := asModel(t, updated)

	if got.modelPicker.active {
		t.Fatal("esc should close the picker")
	}
	if got.profile.ID != original {
		t.Fatalf("esc must not change the model, got %s", got.profile.ID)
	}
}

// A long model name must not shunt the price column out of alignment.
func TestPickerColumnsStayAligned(t *testing.T) {
	reg := &models.Registry{Profiles: []models.Profile{
		{ID: "short", Name: "GLM", CostTier: "local"},
		{ID: "long", Name: "OpenAI GPT-4o Mini (direct build)", CostTier: "cloud-cheap"},
	}}
	p := newModelPicker(reg, stubAvailability{}, "")

	var widths []int
	for _, line := range strings.Split(p.View(), "\n") {
		i := strings.Index(line, "local")
		if i < 0 {
			i = strings.Index(line, "cloud-cheap")
		}
		if i > 0 {
			widths = append(widths, i)
		}
	}
	if len(widths) != 2 {
		t.Fatalf("expected two model rows, got %d", len(widths))
	}
	if widths[0] != widths[1] {
		t.Fatalf("tier column misaligned: %d vs %d", widths[0], widths[1])
	}
}

func TestFitTruncatesAndMarks(t *testing.T) {
	if got := fit("short", 26); got != "short" {
		t.Fatalf("short names should be untouched, got %q", got)
	}
	got := fit(strings.Repeat("x", 40), 26)
	if len([]rune(got)) != 26 {
		t.Fatalf("expected exactly 26 runes, got %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncation should be marked, got %q", got)
	}
}
