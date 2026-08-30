package tools

import (
	"encoding/json"
	"fmt"
)

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

func (tk *Toolkit) Execute(call ToolCall) ToolResult {
	tr := ToolResult{ID: call.ID, Name: call.Name}

	switch call.Name {
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		content, err := tk.ReadFile(args.Path)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		tr.Content = content

	case "list_dir":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if args.Path == "" {
			args.Path = "."
		}
		entries, err := tk.ListDir(args.Path)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		tr.Content = string(data)

	case "grep":
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		if args.Path == "" {
			args.Path = "."
		}
		matches, err := tk.Grep(args.Pattern, args.Path)
		if err != nil {
			tr.Error = err.Error()
			return tr
		}
		data, _ := json.MarshalIndent(matches, "", "  ")
		tr.Content = string(data)

	case "exec":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		res := tk.Exec(args.Command, "")
		data, _ := json.MarshalIndent(res, "", "  ")
		tr.Content = string(data)
		if res.Error != "" {
			tr.Error = res.Error
		}

	case "git_status":
		var args struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		res := tk.GitStatus(args.CWD)
		data, _ := json.MarshalIndent(res, "", "  ")
		tr.Content = string(data)

	case "git_diff":
		var args struct {
			CWD string `json:"cwd"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			tr.Error = fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)
			return tr
		}
		res := tk.GitDiff(args.CWD)
		data, _ := json.MarshalIndent(res, "", "  ")
		tr.Content = string(data)

	default:
		tr.Error = fmt.Sprintf("unknown tool: %s", call.Name)
	}

	return tr
}
