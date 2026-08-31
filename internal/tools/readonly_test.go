package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muzzacode/moz/internal/safepath"
)

func readOnlyToolkit(t *testing.T) (*Toolkit, string) {
	t.Helper()
	dir := t.TempDir()
	tk := New(safepath.New([]string{dir}), nil)
	return tk.ReadOnlyCopy(), dir
}

func call(t *testing.T, name string, args map[string]any) ToolCall {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return ToolCall{ID: "c1", Name: name, Arguments: data}
}

// Every mutating tool must be refused, and crucially must not take effect.
func TestReadOnlyRefusesMutatingTools(t *testing.T) {
	tk, dir := readOnlyToolkit(t)

	target := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(target, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []ToolCall{
		call(t, "write_file", map[string]any{"path": filepath.Join(dir, "new.txt"), "content": "x"}),
		call(t, "edit_file", map[string]any{"path": target, "old_string": "original", "new_string": "hacked"}),
		call(t, "exec", map[string]any{"command": "echo pwned > " + filepath.Join(dir, "shell.txt")}),
		call(t, "git_commit", map[string]any{"cwd": dir}),
		call(t, "add_todo", map[string]any{"text": "should not be added"}),
	}

	for _, c := range cases {
		res := tk.Execute(c)
		if res.Error == "" {
			t.Fatalf("%s should have been refused", c.Name)
		}
		if !strings.Contains(res.Error, "read-only") {
			t.Fatalf("%s error should explain read-only mode, got %q", c.Name, res.Error)
		}
	}

	// Nothing may have changed on disk.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("read-only toolkit modified a file: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("read-only toolkit created a file")
	}
	if _, err := os.Stat(filepath.Join(dir, "shell.txt")); !os.IsNotExist(err) {
		t.Fatal("read-only toolkit executed a shell redirect")
	}
	if tk.Todos.PendingCount() != 0 {
		t.Fatal("read-only toolkit mutated shared todo state")
	}
}

// Aliases must not be a way around the restriction.
func TestReadOnlyRefusesMutatingAliases(t *testing.T) {
	tk, dir := readOnlyToolkit(t)
	for _, alias := range []string{"write", "edit", "replace", "run", "command"} {
		res := tk.Execute(call(t, alias, map[string]any{
			"path":    filepath.Join(dir, "x.txt"),
			"content": "x",
			"command": "true",
		}))
		if res.Error == "" || !strings.Contains(res.Error, "read-only") {
			t.Fatalf("alias %q bypassed read-only enforcement: %+v", alias, res)
		}
	}
}

func TestReadOnlyAllowsInspection(t *testing.T) {
	tk, dir := readOnlyToolkit(t)
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc Target() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if res := tk.Execute(call(t, "read_file", map[string]any{"path": path})); res.Error != "" {
		t.Fatalf("read_file should be allowed: %s", res.Error)
	}
	if res := tk.Execute(call(t, "grep", map[string]any{"pattern": "Target", "path": dir})); res.Error != "" {
		t.Fatalf("grep should be allowed: %s", res.Error)
	}
	if res := tk.Execute(call(t, "find_files", map[string]any{"query": "a.go", "path": dir})); res.Error != "" {
		t.Fatalf("find_files should be allowed: %s", res.Error)
	}
	if res := tk.Execute(call(t, "outline", map[string]any{"path": path})); res.Error != "" {
		t.Fatalf("outline should be allowed: %s", res.Error)
	}
	if res := tk.Execute(call(t, "list_todos", nil)); res.Error != "" {
		t.Fatalf("list_todos should be allowed: %s", res.Error)
	}
}

// A read-only copy must not share the checkpoint store, which is not safe for
// concurrent use by parallel sub-agents.
func TestReadOnlyCopyDropsCheckpointStore(t *testing.T) {
	dir := t.TempDir()
	tk := New(safepath.New([]string{dir}), nil)
	tk.Checkpoints = &countingRecorder{}

	ro := tk.ReadOnlyCopy()
	if ro.Checkpoints != nil {
		t.Fatal("read-only copy must not hold the checkpoint store")
	}
	if tk.Checkpoints == nil {
		t.Fatal("the original toolkit must keep its checkpoint store")
	}
	if tk.ReadOnly {
		t.Fatal("ReadOnlyCopy must not mutate the original")
	}
}

type countingRecorder struct{ n int }

func (c *countingRecorder) Record(string) error { c.n++; return nil }

// Sub-agents must not be offered tools they cannot use, and must not be able to
// spawn further agents.
func TestReadOnlyDefinitionsExcludeMutatingTools(t *testing.T) {
	names := map[string]bool{}
	for _, d := range ReadOnlyDefinitions() {
		names[d.Name] = true
	}

	for _, banned := range []string{"write_file", "edit_file", "exec", "spawn_agents", "add_todo", "mark_done"} {
		if names[banned] {
			t.Fatalf("%s must not be offered to a read-only agent", banned)
		}
	}
	for _, want := range []string{"read_file", "list_dir", "grep", "find_files", "outline", "web_search"} {
		if !names[want] {
			t.Fatalf("%s should be available to a read-only agent", want)
		}
	}
	if len(ReadOnlyDefinitions()) >= len(Definitions()) {
		t.Fatal("read-only definitions should be a strict subset")
	}
}

func TestIsMutatingResolvesAliases(t *testing.T) {
	for _, name := range []string{"write_file", "write", "edit", "exec", "run", "spawn_agents"} {
		if !IsMutating(name) {
			t.Fatalf("%s should be classified as mutating", name)
		}
	}
	for _, name := range []string{"read_file", "read", "grep", "outline", "find_files"} {
		if IsMutating(name) {
			t.Fatalf("%s should not be classified as mutating", name)
		}
	}
}

// The default toolkit must be unaffected by the read-only plumbing.
func TestDefaultToolkitStillMutates(t *testing.T) {
	dir := t.TempDir()
	tk := New(safepath.New([]string{dir}), nil)

	path := filepath.Join(dir, "new.txt")
	if res := tk.Execute(call(t, "write_file", map[string]any{"path": path, "content": "hello"})); res.Error != "" {
		t.Fatalf("write_file should work on a normal toolkit: %s", res.Error)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "hello" {
		t.Fatalf("file not written: %v %q", err, data)
	}
}
