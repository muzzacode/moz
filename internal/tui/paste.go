package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// pasteCollapseLines is the size above which a paste is hidden behind a
// placeholder.
//
// Pasting a large file into the prompt otherwise fills the screen and buries
// both the conversation and what the user was typing. The content is still sent
// in full; only the display is collapsed.
const pasteCollapseLines = 20

// placeholderRe matches a collapsed paste marker.
var placeholderRe = regexp.MustCompile(`\[#(\d+) pasted (\d+) lines\]`)

// pasteStore holds collapsed paste contents for the current input.
type pasteStore struct {
	next  int
	items map[int]string
}

func newPasteStore() pasteStore {
	return pasteStore{items: make(map[int]string)}
}

// collapse stores content and returns the placeholder to show instead.
func (s *pasteStore) collapse(content string) string {
	if s.items == nil {
		s.items = make(map[int]string)
	}
	s.next++
	s.items[s.next] = content
	return fmt.Sprintf("[#%d pasted %d lines]", s.next, countLines(content))
}

// expand replaces placeholders in input with their stored content.
func (s *pasteStore) expand(input string) string {
	if len(s.items) == 0 {
		return input
	}
	return placeholderRe.ReplaceAllStringFunc(input, func(m string) string {
		sub := placeholderRe.FindStringSubmatch(m)
		id, err := strconv.Atoi(sub[1])
		if err != nil {
			return m
		}
		content, ok := s.items[id]
		if !ok {
			// Not ours: leave text the user typed themselves untouched.
			return m
		}
		return content
	})
}

// reset clears stored pastes, for once the input has been submitted.
func (s *pasteStore) reset() {
	s.next = 0
	s.items = make(map[int]string)
}

// shouldCollapse reports whether a paste is large enough to hide.
func shouldCollapse(content string) bool {
	return countLines(content) > pasteCollapseLines
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// Prompt geometry.
const (
	// promptHeight is the visible prompt size. Multi-line so a long message can
	// be reviewed before sending, but kept short so an empty prompt does not
	// dominate the screen. Longer input scrolls within the field.
	promptHeight = 3
	// promptMaxHeight bounds growth so the prompt cannot crowd out the
	// conversation.
	promptMaxHeight = 12
	// promptMarker shows where input begins and marks continuation lines.
	promptMarker = "> "
)

// handlePaste collapses a large paste and inserts a placeholder instead.
//
// It returns false for small pastes so the textarea handles them normally.
func (m *Model) handlePaste(content string) bool {
	if !shouldCollapse(content) {
		return false
	}
	m.textarea.InsertString(m.pastes.collapse(content))
	return true
}
