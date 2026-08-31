package index

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Symbol is a top-level declaration in a source file.
type Symbol struct {
	Line int    `json:"line"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Outline is a file's structure without its bodies.
type Outline struct {
	File     string   `json:"file"`
	Language string   `json:"language"`
	Lines    int      `json:"lines"`
	Symbols  []Symbol `json:"symbols"`
}

// declPattern matches a declaration and captures the symbol name in group 1.
type declPattern struct {
	kind string
	re   *regexp.Regexp
}

// Patterns are deliberately anchored at the start of a line, optionally after
// indentation. Parsing real grammars for every language is out of scope; the
// goal is a reliable map of a file, not a compiler.
var languagePatterns = map[string][]declPattern{
	"go": {
		{"func", regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`)},
		{"type", regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`)},
		{"const", regexp.MustCompile(`^const\s+([A-Za-z_]\w*)`)},
		{"var", regexp.MustCompile(`^var\s+([A-Za-z_]\w*)`)},
	},
	"python": {
		{"class", regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
		{"def", regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`)},
	},
	"javascript": {
		{"class", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][\w$]*)`)},
		{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`)},
		{"const", regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`)},
		{"type", regexp.MustCompile(`^\s*(?:export\s+)?(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)`)},
	},
	"java": {
		{"class", regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?(?:final\s+)?(?:abstract\s+)?class\s+([A-Za-z_]\w*)`)},
		{"interface", regexp.MustCompile(`^\s*(?:public|private|protected)?\s*interface\s+([A-Za-z_]\w*)`)},
		{"record", regexp.MustCompile(`^\s*(?:public|private|protected)?\s*record\s+([A-Za-z_]\w*)`)},
		{"enum", regexp.MustCompile(`^\s*(?:public|private|protected)?\s*enum\s+([A-Za-z_]\w*)`)},
		{"method", regexp.MustCompile(`^\s+(?:public|private|protected)\s+(?:static\s+)?(?:final\s+)?[\w<>\[\],.\s?]+\s+([A-Za-z_]\w*)\s*\(`)},
	},
	"rust": {
		{"fn", regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`)},
		{"struct", regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)`)},
		{"enum", regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)`)},
		{"trait", regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)`)},
		{"impl", regexp.MustCompile(`^\s*impl(?:<[^>]*>)?\s+([A-Za-z_]\w*)`)},
	},
	"shell": {
		{"func", regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_]\w*)\s*\(\)\s*\{`)},
	},
	"make": {
		{"target", regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:(?:[^=]|$)`)},
	},
}

// extLanguage maps a file extension to a pattern set.
var extLanguage = map[string]string{
	".go":   "go",
	".py":   "python",
	".js":   "javascript",
	".jsx":  "javascript",
	".ts":   "javascript",
	".tsx":  "javascript",
	".mjs":  "javascript",
	".java": "java",
	".rs":   "rust",
	".sh":   "shell",
	".bash": "shell",
	".zsh":  "shell",
}

// maxOutlineSymbols caps output so an enormous generated file cannot flood the
// context window.
const maxOutlineSymbols = 400

// DetectLanguage identifies a file's pattern set, or "" when unsupported.
func DetectLanguage(path string) string {
	base := filepath.Base(path)
	if base == "Makefile" || base == "makefile" || base == "GNUmakefile" {
		return "make"
	}
	return extLanguage[strings.ToLower(filepath.Ext(path))]
}

// Outline extracts a file's top-level declarations.
func GetOutline(path string) (*Outline, error) {
	lang := DetectLanguage(path)
	if lang == "" {
		return nil, fmt.Errorf("no outline support for %s", filepath.Base(path))
	}
	patterns := languagePatterns[lang]

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := &Outline{File: path, Language: lang}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBufferSize)

	lineNo := 0
	inBlockComment := false

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		// Skip comments so a commented-out function is not reported as real.
		trimmed := strings.TrimSpace(line)
		if inBlockComment {
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			if !strings.Contains(trimmed, "*/") {
				inBlockComment = true
			}
			continue
		}
		if isCommentLine(trimmed, lang) {
			continue
		}

		if len(out.Symbols) >= maxOutlineSymbols {
			continue
		}
		for _, p := range patterns {
			m := p.re.FindStringSubmatch(line)
			if m == nil || len(m) < 2 || m[1] == "" {
				continue
			}
			out.Symbols = append(out.Symbols, Symbol{Line: lineNo, Kind: p.kind, Name: m[1]})
			break
		}
	}
	out.Lines = lineNo
	return out, nil
}

func isCommentLine(trimmed, lang string) bool {
	switch lang {
	case "python", "shell", "make":
		return strings.HasPrefix(trimmed, "#")
	default:
		return strings.HasPrefix(trimmed, "//")
	}
}

// Render formats an outline as compact text for the model.
func (o *Outline) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s, %d lines, %d symbols)\n", o.File, o.Language, o.Lines, len(o.Symbols))
	if len(o.Symbols) == 0 {
		b.WriteString("  (no top-level symbols found)\n")
		return b.String()
	}
	for _, s := range o.Symbols {
		fmt.Fprintf(&b, "  %5d  %-9s %s\n", s.Line, s.Kind, s.Name)
	}
	if len(o.Symbols) >= maxOutlineSymbols {
		b.WriteString("  ... (symbol list truncated)\n")
	}
	return b.String()
}
