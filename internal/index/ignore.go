// Package index provides repository-aware file discovery, search, and symbol
// outlines so the agent can work in large codebases without reading everything.
package index

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultIgnores are directories that are never worth searching. Walking them
// is pure waste: in a bare Moz checkout .git alone is 88% of all files, and a
// JavaScript project's node_modules dwarfs the actual source.
var defaultIgnores = map[string]bool{
	".git":          true,
	".hg":           true,
	".svn":          true,
	"node_modules":  true,
	"vendor":        true,
	"target":        true,
	"dist":          true,
	"build":         true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	".gradle":       true,
	".idea":         true,
	".vscode":       true,
	".next":         true,
	".nuxt":         true,
	".terraform":    true,
	"coverage":      true,
	".DS_Store":     true,
}

// rule is one parsed ignore pattern.
type rule struct {
	pattern string
	negate  bool
	dirOnly bool
	// anchored patterns match from the ignore file's directory rather than at
	// any depth.
	anchored bool
}

// Matcher decides whether a path should be skipped.
type Matcher struct {
	root  string
	rules []rule
	// extraIgnores holds caller-supplied directory names.
	extraIgnores map[string]bool
}

// NewMatcher builds a matcher for root, loading .gitignore and .mozignore when
// present. Missing ignore files are not an error.
func NewMatcher(root string) *Matcher {
	m := &Matcher{root: filepath.Clean(root), extraIgnores: map[string]bool{}}
	for _, name := range []string{".gitignore", ".mozignore"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			m.rules = append(m.rules, parseIgnoreFile(string(data))...)
		}
	}
	return m
}

// AddIgnoreDir marks an additional directory name as ignored at any depth.
func (m *Matcher) AddIgnoreDir(name string) {
	if name != "" {
		m.extraIgnores[name] = true
	}
}

func parseIgnoreFile(content string) []rule {
	var rules []rule
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		r := rule{pattern: trimmed}
		if strings.HasPrefix(r.pattern, "!") {
			r.negate = true
			r.pattern = r.pattern[1:]
		}
		if strings.HasSuffix(r.pattern, "/") {
			r.dirOnly = true
			r.pattern = strings.TrimSuffix(r.pattern, "/")
		}
		// A pattern containing a slash anywhere but the end is anchored to the
		// ignore file's directory, per gitignore semantics.
		if strings.Contains(r.pattern, "/") {
			r.anchored = true
			r.pattern = strings.TrimPrefix(r.pattern, "/")
		}
		if r.pattern == "" {
			continue
		}
		rules = append(rules, r)
	}
	return rules
}

// SkipDir reports whether a directory should not be descended into.
func (m *Matcher) SkipDir(path string, name string) bool {
	if defaultIgnores[name] || m.extraIgnores[name] {
		return true
	}
	return m.matches(path, name, true)
}

// SkipFile reports whether a file should be excluded.
func (m *Matcher) SkipFile(path string, name string) bool {
	if defaultIgnores[name] {
		return true
	}
	return m.matches(path, name, false)
}

// matches evaluates the rule list. Later rules win, which is how gitignore
// negation is able to re-include a path excluded earlier.
func (m *Matcher) matches(path, name string, isDir bool) bool {
	rel := m.relative(path)
	ignored := false
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.match(rel, name) {
			ignored = !r.negate
		}
	}
	return ignored
}

func (m *Matcher) relative(path string) string {
	rel, err := filepath.Rel(m.root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func (r rule) match(rel, name string) bool {
	if r.anchored {
		if ok, _ := filepath.Match(r.pattern, rel); ok {
			return true
		}
		// A directory pattern also covers everything beneath it.
		return strings.HasPrefix(rel, strings.TrimSuffix(r.pattern, "/*")+"/")
	}

	// Unanchored patterns match the basename at any depth.
	if ok, _ := filepath.Match(r.pattern, name); ok {
		return true
	}
	// Also test each path segment so "build" excludes "a/build/c".
	for _, seg := range strings.Split(rel, "/") {
		if ok, _ := filepath.Match(r.pattern, seg); ok {
			return true
		}
	}
	return false
}
