package tools

// Definition describes a tool for the model.
type Definition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Definitions returns the tools Moz can use.
// mutatingTools can change the workspace or the outside world. They are denied
// to read-only toolkits, which is what sub-agents run with.
//
// exec is included because it can do anything, and git_commit because it alters
// history. Sub-agents exist to investigate in parallel; letting several of them
// write concurrently invites corrupted files and unreviewable changes.
var mutatingTools = map[string]bool{
	"write_file": true,
	"edit_file":  true,
	"exec":       true,
	"git_commit": true,
	"git_push":   true,
	"git_pull":   true,
	// Todos are shared parent state, so sub-agents must not rewrite the plan.
	"add_todo":  true,
	"mark_done": true,
	// Nested spawning would allow unbounded fan-out.
	"spawn_agents": true,
}

// IsMutating reports whether a tool is denied in read-only mode.
func IsMutating(name string) bool {
	return mutatingTools[resolveToolName(name)]
}

// ReadOnlyDefinitions returns the tools a sub-agent may call.
func ReadOnlyDefinitions() []Definition {
	all := Definitions()
	out := make([]Definition, 0, len(all))
	for _, d := range all {
		if !mutatingTools[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

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
			Name:        "spawn_agents",
			Description: "Run up to 6 independent read-only investigations in parallel and get back their findings. Use this when a task needs several separate questions answered, for example understanding three unrelated subsystems. Each sub-agent has its own context, so this keeps large amounts of reading out of your conversation. Sub-agents cannot modify anything, so do the editing yourself afterwards. Do not use this for a single question you could answer with one or two tool calls.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tasks": map[string]interface{}{
						"type":        "array",
						"description": "Self-contained questions. Each must include the context needed to answer it, because sub-agents cannot see your conversation.",
						"items":       map[string]string{"type": "string"},
					},
				},
				"required": []string{"tasks"},
			},
		},
		{
			Name:        "find_files",
			Description: "Find files by name or glob, ranked by relevance. Use this to locate a file instead of grepping for it. Respects .gitignore.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]string{
						"type":        "string",
						"description": "Filename, fragment, or glob such as *_test.go",
					},
					"path": map[string]string{
						"type":        "string",
						"description": "Directory to search. Defaults to the current directory.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "outline",
			Description: "List the top-level declarations in a source file with line numbers. Use this to understand a large file before reading it. Supports Go, Python, JavaScript, TypeScript, Java, Rust, shell, and Makefiles.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]string{
						"type":        "string",
						"description": "File to outline",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "grep",
			Description: "Search file contents with a regular expression. Skips ignored directories and binary files, and caps results. Use find_files to locate a file by name instead.",
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
					"include": map[string]string{
						"type":        "string",
						"description": "Optional glob restricting which files are searched, such as *.go",
					},
					"ignore_case": map[string]any{
						"type":        "boolean",
						"description": "Match case-insensitively. Defaults to false.",
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
