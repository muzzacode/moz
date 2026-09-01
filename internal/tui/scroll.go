package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleScrollKey routes history-navigation keys to the viewport.
//
// The textarea consumes every key it is given, so without this the conversation
// could not be scrolled at all. Plain arrow keys are deliberately left to the
// input for cursor movement; scrolling uses keys that have no meaning in a short
// text field.
func (m *Model) handleScrollKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "pgup", "pgdown", "shift+up", "shift+down", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return true, cmd

	case "home":
		m.viewport.GotoTop()
		return true, nil

	case "end":
		m.viewport.GotoBottom()
		return true, nil
	}
	return false, nil
}

// handlePickerKey drives the model picker while it is open.
func (m *Model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "ctrl+p":
		m.modelPicker.moveBy(-1)

	case "down", "j", "ctrl+n":
		m.modelPicker.moveBy(1)

	case "pgup":
		m.modelPicker.moveBy(-visibleRows)

	case "pgdown":
		m.modelPicker.moveBy(visibleRows)

	case "home":
		m.modelPicker.moveBy(-len(m.modelPicker.items))

	case "end":
		m.modelPicker.moveBy(len(m.modelPicker.items))

	case "enter":
		return m.applyPickerSelection()

	case "esc", "ctrl+c", "q":
		m.modelPicker.active = false
		m.updateViewport()
		return m, nil
	}

	m.updateViewport()
	return m, nil
}

// applyPickerSelection switches to the highlighted model.
func (m *Model) applyPickerSelection() (tea.Model, tea.Cmd) {
	item, ok := m.modelPicker.selected()
	m.modelPicker.active = false

	if !ok {
		m.updateViewport()
		return m, nil
	}

	p, err := m.registry.Find(item.id)
	if err != nil {
		m.errMsg = err.Error()
		m.updateViewport()
		return m, nil
	}

	m.profile = p
	// Choosing explicitly means the user wants this model, so adaptive routing
	// must stop overriding it.
	m.mode = "manual"
	m.errMsg = ""

	msg := "Switched to " + p.Name
	// Selecting an unusable model would otherwise fail only at the next request,
	// long after the choice was made.
	if !item.available {
		msg += " — warning: no API key found for this model"
	}
	m.addSystem(msg)
	m.updateViewport()
	return m, nil
}
