// Package diff renders human-readable previews of pending file changes.
//
// Approval prompts previously showed raw tool JSON, which made it impossible to
// tell what a change would actually do. Every destructive edit now gets a
// line-numbered preview instead.
package diff

import (
	"fmt"
	"strings"
)

// ContextLines is how many unchanged lines to show around a change.
const ContextLines = 3

// maxPreviewLines caps a preview so a huge replacement cannot flood the UI.
const maxPreviewLines = 60

// PreviewEdit renders the effect of replacing oldStr with newStr in original.
func PreviewEdit(path, original, oldStr, newStr string, replaceAll bool) string {
	if oldStr == "" {
		return header(path) + "\n  (invalid: empty old_string)"
	}

	count := strings.Count(original, oldStr)
	switch {
	case count == 0:
		return header(path) + "\n  (old_string not found; this edit will fail)"
	case count > 1 && !replaceAll:
		return fmt.Sprintf("%s\n  (old_string matches %d times; this edit will fail without replace_all)", header(path), count)
	}

	idx := strings.Index(original, oldStr)
	startLine := 1 + strings.Count(original[:idx], "\n")

	removed := strings.Split(oldStr, "\n")
	added := strings.Split(newStr, "\n")
	lines := strings.Split(original, "\n")

	var b strings.Builder
	b.WriteString(header(path))
	if replaceAll && count > 1 {
		fmt.Fprintf(&b, " (%d occurrences)", count)
	}
	b.WriteString("\n")

	// Leading context.
	ctxStart := startLine - ContextLines
	if ctxStart < 1 {
		ctxStart = 1
	}
	for i := ctxStart; i < startLine; i++ {
		fmt.Fprintf(&b, "  %4d   %s\n", i, lines[i-1])
	}

	writeChangeBlock(&b, removed, added)

	// Trailing context.
	afterLine := startLine + len(removed)
	for i := afterLine; i < afterLine+ContextLines && i <= len(lines); i++ {
		fmt.Fprintf(&b, "  %4d   %s\n", i, lines[i-1])
	}

	return strings.TrimRight(b.String(), "\n")
}

// writeChangeBlock emits removed then added lines, budgeting the line cap
// between them so neither side is entirely hidden.
func writeChangeBlock(b *strings.Builder, removed, added []string) {
	half := maxPreviewLines / 2
	for _, l := range limit(removed, half) {
		fmt.Fprintf(b, "  %4s - %s\n", "", l)
	}
	for _, l := range limit(added, half) {
		fmt.Fprintf(b, "  %4s + %s\n", "", l)
	}
}

// PreviewWrite renders a new file's contents.
func PreviewWrite(path, content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "create %s (%d lines)\n", path, len(lines))
	for i, l := range limit(lines, maxPreviewLines) {
		fmt.Fprintf(&b, "  %4d + %s\n", i+1, l)
	}
	return strings.TrimRight(b.String(), "\n")
}

func header(path string) string {
	return "edit " + path
}

// limit truncates a slice and appends an elision marker when it is too long.
func limit(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	out := make([]string, 0, max+1)
	out = append(out, lines[:max]...)
	out = append(out, fmt.Sprintf("... (%d more lines)", len(lines)-max))
	return out
}
