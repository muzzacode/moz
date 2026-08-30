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
