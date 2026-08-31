package index

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Search limits. These exist to protect the context window as much as the
// clock: an unbounded grep across a large repo can return tens of thousands of
// lines and destroy the conversation.
const (
	DefaultMaxResults    = 200
	DefaultMaxPerFile    = 20
	maxFileSize          = 2 << 20 // 2 MiB
	binarySniffBytes     = 8000
	maxLineLen           = 400
	maxScannerBufferSize = 1 << 20
)

type Match struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type SearchResult struct {
	Matches []Match `json:"matches"`
	// FilesScanned and FilesSkipped explain the search's reach, which matters
	// when a result set looks surprisingly small.
	FilesScanned int `json:"files_scanned"`
	FilesSkipped int `json:"files_skipped"`
	// Truncated reports that limits were hit and more matches exist.
	Truncated bool `json:"truncated"`
}

type SearchOptions struct {
	// Include restricts the search to paths matching this glob.
	Include string
	// MaxResults caps total matches. Zero uses DefaultMaxResults.
	MaxResults int
	// MaxPerFile caps matches from a single file so one dense file cannot
	// crowd out every other result.
	MaxPerFile int
	// IgnoreCase performs a case-insensitive match.
	IgnoreCase bool
}

func (o SearchOptions) maxResults() int {
	if o.MaxResults <= 0 {
		return DefaultMaxResults
	}
	return o.MaxResults
}

func (o SearchOptions) maxPerFile() int {
	if o.MaxPerFile <= 0 {
		return DefaultMaxPerFile
	}
	return o.MaxPerFile
}

// Search finds pattern under root, honouring ignore rules and result caps.
func Search(root, pattern string, opts SearchOptions) (*SearchResult, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("search pattern is empty")
	}

	expr := pattern
	if opts.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}

	res := &SearchResult{}

	// Searching a single file is a common and cheap case.
	if !info.IsDir() {
		matches, _, err := searchFile(re, root, opts.maxPerFile())
		if err != nil {
			return nil, err
		}
		res.Matches = matches
		res.FilesScanned = 1
		return res, nil
	}

	matcher := NewMatcher(root)
	limit := opts.maxResults()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// An unreadable directory should not abort the whole search.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(res.Matches) >= limit {
			return fs.SkipAll
		}

		if d.IsDir() {
			if path == root {
				return nil
			}
			if matcher.SkipDir(path, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}

		if matcher.SkipFile(path, d.Name()) {
			res.FilesSkipped++
			return nil
		}
		if opts.Include != "" && !matchesGlob(opts.Include, path, d.Name()) {
			return nil
		}

		remaining := limit - len(res.Matches)
		perFile := opts.maxPerFile()
		if remaining < perFile {
			perFile = remaining
		}

		matches, skipped, err := searchFile(re, path, perFile)
		if err != nil || skipped {
			res.FilesSkipped++
			return nil
		}
		res.FilesScanned++
		res.Matches = append(res.Matches, matches...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(res.Matches) >= limit {
		res.Truncated = true
	}
	return res, nil
}

// searchFile scans one file. It reports skipped=true for files that are not
// worth searching, such as binaries and very large files.
func searchFile(re *regexp.Regexp, path string, maxPerFile int) (matches []Match, skipped bool, err error) {
	if maxPerFile <= 0 {
		return nil, false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, true, err
	}
	if info.Size() > maxFileSize {
		return nil, true, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, true, err
	}
	defer f.Close()

	// Sniff for binary content before running a regex over it. Matching against
	// git objects or compiled artifacts produces unreadable noise.
	head := make([]byte, binarySniffBytes)
	n, _ := f.Read(head)
	if isBinary(head[:n]) {
		return nil, true, nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, true, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBufferSize)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !re.MatchString(line) {
			continue
		}
		matches = append(matches, Match{
			File:    path,
			Line:    lineNo,
			Content: clipLine(strings.TrimSpace(line)),
		})
		if len(matches) >= maxPerFile {
			break
		}
	}
	// A scanner error mid-file still yields the matches found so far.
	return matches, false, nil
}

// isBinary treats a NUL byte as the signal, which is the same heuristic git and
// ripgrep use.
func isBinary(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0
}

func clipLine(s string) string {
	if len(s) <= maxLineLen {
		return s
	}
	return s[:maxLineLen] + "…"
}

// matchesGlob tests a glob against both the basename and the full path, so
// "*.go" and "internal/**" both behave as expected.
func matchesGlob(glob, path, name string) bool {
	if ok, _ := filepath.Match(glob, name); ok {
		return true
	}
	if ok, _ := filepath.Match(glob, filepath.ToSlash(path)); ok {
		return true
	}
	// Support a trailing ** as a prefix match.
	if strings.HasSuffix(glob, "**") {
		prefix := strings.TrimSuffix(glob, "**")
		return strings.Contains(filepath.ToSlash(path), prefix)
	}
	return strings.Contains(filepath.ToSlash(path), glob)
}

// FindFiles locates files whose path or name matches query, ranked so the most
// plausible candidates come first.
//
// Locating a file by fragment of its name is one of the most common things an
// agent needs, and doing it with grep is both slow and noisy.
func FindFiles(root, query string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, fmt.Errorf("query is empty")
	}

	matcher := NewMatcher(root)

	type scored struct {
		path  string
		score int
	}
	var found []scored

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && matcher.SkipDir(path, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if matcher.SkipFile(path, d.Name()) {
			return nil
		}

		name := strings.ToLower(d.Name())
		rel := strings.ToLower(filepath.ToSlash(relOrPath(root, path)))

		score, ok := scoreMatch(q, name, rel)
		if !ok {
			return nil
		}
		found = append(found, scored{path: path, score: score})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].score != found[j].score {
			return found[i].score > found[j].score
		}
		// Shorter paths are usually the more relevant match.
		return len(found[i].path) < len(found[j].path)
	})

	out := make([]string, 0, limit)
	for i, f := range found {
		if i >= limit {
			break
		}
		out = append(out, f.path)
	}
	return out, nil
}

// scoreMatch ranks a candidate. Exact and prefix name matches rank above
// substring matches, and a name match always beats a path-only match.
func scoreMatch(q, name, rel string) (int, bool) {
	// A glob query is treated as a pattern rather than a substring.
	if strings.ContainsAny(q, "*?") {
		if ok, _ := filepath.Match(q, name); ok {
			return 90, true
		}
		if ok, _ := filepath.Match(q, rel); ok {
			return 70, true
		}
		return 0, false
	}

	switch {
	case name == q:
		return 100, true
	case strings.HasPrefix(name, q):
		return 80, true
	case strings.Contains(name, q):
		return 60, true
	case strings.Contains(rel, q):
		return 40, true
	default:
		return 0, false
	}
}

func relOrPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}
