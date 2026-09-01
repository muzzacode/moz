// Package project loads repository-specific instructions so the agent follows a
// project's own conventions.
//
// Real repositories carry knowledge that cannot be inferred from the source: a
// required toolchain version, an environment variable that must be set before
// the build works, a migration convention. Without reading these files the agent
// confidently runs the wrong commands.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// candidates are the instruction files to look for, in priority order.
//
// AGENTS.md is the emerging cross-tool convention. The others are supported so
// an existing repository works without being modified for Moz.
var candidates = []string{
	"AGENTS.md",
	".mozrules",
	"CLAUDE.md",
	".cursorrules",
	"CONVENTIONS.md",
}

// maxInstructionBytes caps how much of an instruction file is injected. These
// files are occasionally enormous, and the system prompt competes with the
// conversation for context.
const maxInstructionBytes = 6000

type Instructions struct {
	// Source is the file the content came from, for display.
	Source string
	// Content is the instruction text, possibly truncated.
	Content string
	// Truncated reports that the file was longer than the cap.
	Truncated bool
}

// Load reads the first instruction file found in dir.
//
// A missing file is not an error: most projects do not have one.
func Load(dir string) (*Instructions, bool) {
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}

		ins := &Instructions{Source: name}
		if len(content) > maxInstructionBytes {
			ins.Content = content[:maxInstructionBytes]
			ins.Truncated = true
		} else {
			ins.Content = content
		}
		return ins, true
	}
	return nil, false
}

// Render formats the instructions for inclusion in a system prompt.
func (i *Instructions) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project instructions from %s. Follow these; they override your defaults:\n\n", i.Source)
	b.WriteString(i.Content)
	if i.Truncated {
		b.WriteString("\n\n[instructions truncated]")
	}
	return b.String()
}
