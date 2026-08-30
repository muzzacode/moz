package tools

// Definition describes a tool for the model.
type Definition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Definitions returns the tools Moz can use.
func Definitions() []Definition {
	return []Definition{
		{
			Name:        "read_file",
			Description: "Read the contents of a file. Use this when you need to inspect code, configuration, or documentation.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]string{
						"type":        "string",
						"description": "Absolute or relative path to the file",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "list_dir",
			Description: "List the files and directories at a path. Use this to understand project structure.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]string{
						"type":        "string",
						"description": "Directory path to list. Defaults to current directory.",
					},
				},
			},
		},
		{
			Name:        "grep",
			Description: "Search for a regex pattern in files. Use this to find symbols, usages, or references.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]string{
						"type":        "string",
						"description": "Regular expression pattern to search for",
					},
					"path": map[string]string{
						"type":        "string",
						"description": "File or directory to search. Defaults to current directory.",
					},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "exec",
			Description: "Run a shell command. Use this for git, tests, builds, or any command-line operation. Always prefer read/list/grep before running commands.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]string{
						"type":        "string",
						"description": "The shell command to execute",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "add_todo",
			Description: "Add a task to the session todo list. Use this when the user asks for a plan or when breaking a multi-step task into steps.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text": map[string]string{
						"type":        "string",
						"description": "The todo text",
					},
				},
				"required": []string{"text"},
			},
		},
		{
			Name:        "list_todos",
			Description: "List all session todos.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "mark_done",
			Description: "Mark a session todo as done.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]string{
						"type":        "string",
						"description": "The todo ID or a prefix of it",
					},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch the content of a URL and return it as readable text. Use this when you need to read a specific web page.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]string{
						"type":        "string",
						"description": "The URL to fetch",
					},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "git_status",
			Description: "Show the git status of a repository.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"cwd": map[string]string{
						"type":        "string",
						"description": "Working directory of the git repo. Defaults to current directory.",
					},
				},
			},
		},
		{
			Name:        "write_file",
			Description: "Create a new file. Fails if the file already exists; use edit_file for existing files.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]string{
						"type":        "string",
						"description": "File path",
					},
					"content": map[string]string{
						"type":        "string",
						"description": "Full file content",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "edit_file",
			Description: "Replace an exact unique string in a file. Include enough surrounding context so old_string matches once. Set replace_all only when every occurrence should change.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]string{
						"type":        "string",
						"description": "File path",
					},
					"old_string": map[string]string{
						"type":        "string",
						"description": "Exact existing text to replace. Must be unique unless replace_all is true.",
					},
					"new_string": map[string]string{
						"type":        "string",
						"description": "New text to insert",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "Replace every occurrence of old_string. Defaults to false.",
					},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
		},
		{
			Name:        "web_search",
			Description: "Search the web for a query. Use this to look up documentation, recent facts, or external resources.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]string{
						"type":        "string",
						"description": "The search query",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "git_diff",
			Description: "Show the git diff of a repository.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"cwd": map[string]string{
						"type":        "string",
						"description": "Working directory of the git repo. Defaults to current directory.",
					},
				},
			},
		},
	}
}
