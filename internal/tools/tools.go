package tools

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/muzzacode/moz/internal/safepath"
	"github.com/muzzacode/moz/internal/todo"
)

type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

type FileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

type Match struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type Toolkit struct {
	Safe  *safepath.Policy
	Todos *todo.List
}

func New(safe *safepath.Policy, todos *todo.List) *Toolkit {
	if todos == nil {
		todos = todo.New()
	}
	return &Toolkit{Safe: safe, Todos: todos}
}

func (tk *Toolkit) ReadFile(path string) (string, error) {
	p, err := tk.Safe.Resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (tk *Toolkit) WriteFile(path, content string) error {
	p, err := tk.Safe.Resolve(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("%s already exists; use edit_file for targeted changes", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	return writeAtomic(p, []byte(content), 0644)
}

func (tk *Toolkit) EditFile(path, oldStr, newStr string) error {
	return tk.editFile(path, oldStr, newStr, false)
}

func (tk *Toolkit) EditFileAll(path, oldStr, newStr string) error {
	return tk.editFile(path, oldStr, newStr, true)
}

func (tk *Toolkit) editFile(path, oldStr, newStr string, replaceAll bool) error {
	if oldStr == "" {
		return fmt.Errorf("old_string must not be empty")
	}
	if oldStr == newStr {
		return fmt.Errorf("old_string and new_string must differ")
	}
	p, err := tk.Safe.Resolve(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	count := strings.Count(string(data), oldStr)
	if count == 0 {
		return fmt.Errorf("old string not found in %s", path)
	}
	if count > 1 && !replaceAll {
		return fmt.Errorf("old string occurs %d times in %s; include more context or set replace_all", count, path)
	}
	limit := 1
	if replaceAll {
		limit = -1
	}
	updated := strings.Replace(string(data), oldStr, newStr, limit)
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	return writeAtomic(p, []byte(updated), info.Mode().Perm())
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".moz-edit-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (tk *Toolkit) ListDir(path string) ([]FileInfo, error) {
	p, err := tk.Safe.Resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	var out []FileInfo
	for _, e := range entries {
		info, err := e.Info()
		size := int64(0)
		if err == nil {
			size = info.Size()
		}
		out = append(out, FileInfo{
			Name: e.Name(),
			Path: filepath.Join(p, e.Name()),
			Dir:  e.IsDir(),
			Size: size,
		})
	}
	return out, nil
}

func (tk *Toolkit) Grep(pattern, path string) ([]Match, error) {
	p, err := tk.Safe.Resolve(path)
	if err != nil {
		return nil, err
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	var matches []Match

	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		err = filepath.WalkDir(p, func(walkPath string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			fileMatches, err := grepFile(re, walkPath)
			if err != nil {
				return nil // skip unreadable files
			}
			matches = append(matches, fileMatches...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		fileMatches, err := grepFile(re, p)
		if err != nil {
			return nil, err
		}
		matches = append(matches, fileMatches...)
	}

	return matches, nil
}

func grepFile(re *regexp.Regexp, path string) ([]Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var matches []Match
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if re.MatchString(line) {
			matches = append(matches, Match{
				File:    path,
				Line:    lineNo,
				Content: strings.TrimSpace(line),
			})
		}
	}
	return matches, scanner.Err()
}

func (tk *Toolkit) Exec(command, cwd string) Result {
	var cmd *exec.Cmd
	if cwd != "" {
		cmd = exec.Command("sh", "-c", command)
		cmd.Dir = cwd
	} else {
		cmd = exec.Command("sh", "-c", command)
	}

	out, err := cmd.CombinedOutput()
	res := Result{Stdout: string(out)}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
			res.Error = err.Error()
		}
	}
	if len(res.Stdout) > 8000 {
		res.Stdout = res.Stdout[:8000] + "\n... (truncated)"
	}
	return res
}

func (tk *Toolkit) GitStatus(cwd string) Result {
	return tk.Exec("git status --short", cwd)
}

func (tk *Toolkit) GitDiff(cwd string) Result {
	return tk.Exec("git diff", cwd)
}

func (tk *Toolkit) GitCommit(cwd, message string) Result {
	return tk.Exec(fmt.Sprintf("git commit -m %q", message), cwd)
}

// LimitRead prevents huge files from flooding the model.
func LimitRead(r io.Reader, max int) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, int64(max)))
}
