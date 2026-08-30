package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/muzzacode/moz/internal/diff"
	"github.com/muzzacode/moz/internal/llm"
)

// describeToolCall renders a tool call for the approval prompt.
//
// File mutations get a line-numbered preview of the actual change, because raw
// tool JSON makes it impossible to see what a change will really do.
func (m *Model) describeToolCall(tc *llm.ToolCall) string {
	switch tc.Name {
	case "edit_file":
		return m.describeEdit(tc)
	case "write_file":
		return m.describeWrite(tc)
	case "exec":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err == nil && args.Command != "" {
			return "run: " + args.Command
		}
	}
	return fmt.Sprintf("%s(%s)", tc.Name, truncate(string(tc.Arguments), 400))
}

func (m *Model) describeEdit(tc *llm.ToolCall) string {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		Old        string `json:"old"`
		New        string `json:"new"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil || args.Path == "" {
		return fmt.Sprintf("edit_file(%s)", truncate(string(tc.Arguments), 400))
	}
	oldStr := firstNonEmpty(args.OldString, args.Old)
	newStr := firstNonEmpty(args.NewString, args.New)

	original, err := m.toolkit.ReadFile(args.Path)
	if err != nil {
		return fmt.Sprintf("edit %s (cannot read file: %v)", args.Path, err)
	}
	return diff.PreviewEdit(args.Path, original, oldStr, newStr, args.ReplaceAll)
}

func (m *Model) describeWrite(tc *llm.ToolCall) string {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil || args.Path == "" {
		return fmt.Sprintf("write_file(%s)", truncate(string(tc.Arguments), 400))
	}
	// An existing file signals a likely mistake, since write_file refuses to
	// overwrite. Surface that before the user approves.
	if _, err := m.toolkit.ReadFile(args.Path); err == nil {
		return fmt.Sprintf("write %s\n  (file already exists; this will fail, edit_file is required)", args.Path)
	}
	return diff.PreviewWrite(args.Path, args.Content)
}

// formatConfirm renders an approval prompt. Multi-line previews put the
// question on its own line so the diff stays readable.
func formatConfirm(desc string) string {
	if strings.Contains(desc, "\n") {
		return desc + "\n\nApprove? [y/n]"
	}
	return fmt.Sprintf("Allow %s? [y/n]", desc)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
