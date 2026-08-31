package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkfile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// repo builds a tree that mirrors the real waste: source alongside .git and
// node_modules containing the same search term.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkfile(t, dir, "main.go", "package main\n\nfunc target() {}\n")
	mkfile(t, dir, "internal/app/service.go", "package app\n\nfunc target() {}\n")
	mkfile(t, dir, ".git/objects/ab/cdef", "target binary junk")
	mkfile(t, dir, "node_modules/lib/index.js", "function target() {}")
	mkfile(t, dir, "vendor/dep/dep.go", "func target() {}")
	mkfile(t, dir, "dist/bundle.js", "function target() {}")
	return dir
}

func TestSearchSkipsGitAndNodeModules(t *testing.T) {
	dir := repo(t)
	res, err := Search(dir, "target", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Matches {
		for _, banned := range []string{".git", "node_modules", "vendor", "dist"} {
			if strings.Contains(m.File, banned) {
				t.Fatalf("search must not descend into %s: %s", banned, m.File)
			}
		}
	}
	if len(res.Matches) != 2 {
		t.Fatalf("expected 2 source matches, got %d: %+v", len(res.Matches), res.Matches)
	}
}

func TestSearchHonoursGitignore(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, ".gitignore", "generated/\n*.pb.go\n")
	mkfile(t, dir, "real.go", "func target() {}")
	mkfile(t, dir, "generated/gen.go", "func target() {}")
	mkfile(t, dir, "api.pb.go", "func target() {}")

	res, err := Search(dir, "target", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatalf("expected only real.go, got %+v", res.Matches)
	}
	if !strings.HasSuffix(res.Matches[0].File, "real.go") {
		t.Fatalf("unexpected match: %s", res.Matches[0].File)
	}
}

// Negation must be able to re-include a path excluded by an earlier rule.
func TestGitignoreNegation(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, ".gitignore", "*.log\n!keep.log\n")
	mkfile(t, dir, "drop.log", "target")
	mkfile(t, dir, "keep.log", "target")

	res, err := Search(dir, "target", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || !strings.HasSuffix(res.Matches[0].File, "keep.log") {
		t.Fatalf("negation not honoured: %+v", res.Matches)
	}
}

func TestSearchSkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "text.go", "target here")
	// A NUL byte marks the file as binary.
	mkfile(t, dir, "blob.bin", "target\x00\x01\x02binary")

	res, err := Search(dir, "target", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Matches {
		if strings.HasSuffix(m.File, ".bin") {
			t.Fatal("binary files must not be searched")
		}
	}
	if len(res.Matches) != 1 {
		t.Fatalf("expected 1 match, got %+v", res.Matches)
	}
}

// An unbounded result set would destroy the context window.
func TestSearchCapsTotalResults(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("target\n")
	}
	for i := 0; i < 20; i++ {
		mkfile(t, dir, filepath.Join("pkg", "f"+string(rune('a'+i%26))+".go"), sb.String())
	}

	res, err := Search(dir, "target", SearchOptions{MaxResults: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) > 50 {
		t.Fatalf("result cap ignored: %d matches", len(res.Matches))
	}
	if !res.Truncated {
		t.Fatal("expected Truncated to be reported")
	}
}

// One dense file must not crowd out results from every other file.
func TestSearchCapsPerFile(t *testing.T) {
	dir := t.TempDir()
	dense := strings.Repeat("target\n", 100)
	mkfile(t, dir, "dense.go", dense)
	mkfile(t, dir, "sparse.go", "target once")

	res, err := Search(dir, "target", SearchOptions{MaxPerFile: 3, MaxResults: 100})
	if err != nil {
		t.Fatal(err)
	}
	perFile := map[string]int{}
	for _, m := range res.Matches {
		perFile[filepath.Base(m.File)]++
	}
	if perFile["dense.go"] > 3 {
		t.Fatalf("per-file cap ignored: %d", perFile["dense.go"])
	}
	if perFile["sparse.go"] != 1 {
		t.Fatal("sparse file should still be represented")
	}
}

func TestSearchIncludeGlob(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "a.go", "target")
	mkfile(t, dir, "b.js", "target")

	res, err := Search(dir, "target", SearchOptions{Include: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || !strings.HasSuffix(res.Matches[0].File, "a.go") {
		t.Fatalf("include glob not applied: %+v", res.Matches)
	}
}

func TestSearchIgnoreCase(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "a.go", "TargetValue")

	if res, _ := Search(dir, "targetvalue", SearchOptions{}); len(res.Matches) != 0 {
		t.Fatal("case-sensitive search should not match")
	}
	res, err := Search(dir, "targetvalue", SearchOptions{IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 {
		t.Fatal("case-insensitive search should match")
	}
}

func TestSearchInvalidPattern(t *testing.T) {
	if _, err := Search(t.TempDir(), "([", SearchOptions{}); err == nil {
		t.Fatal("expected an error for an invalid regex")
	}
	if _, err := Search(t.TempDir(), "  ", SearchOptions{}); err == nil {
		t.Fatal("expected an error for an empty pattern")
	}
}

func TestSearchSingleFile(t *testing.T) {
	dir := t.TempDir()
	p := mkfile(t, dir, "one.go", "alpha\ntarget\nbeta\n")

	res, err := Search(p, "target", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Line != 2 {
		t.Fatalf("unexpected result: %+v", res.Matches)
	}
}

func TestFindFilesRanksExactNameFirst(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "deep/nested/config.go", "x")
	mkfile(t, dir, "configuration_helper.go", "x")
	mkfile(t, dir, "other/config.go", "x")

	got, err := FindFiles(dir, "config.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected results")
	}
	if filepath.Base(got[0]) != "config.go" {
		t.Fatalf("exact name should rank first, got %s", got[0])
	}
}

func TestFindFilesSkipsIgnoredDirs(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "app.go", "x")
	mkfile(t, dir, "node_modules/app.go", "x")
	mkfile(t, dir, ".git/app.go", "x")

	got, err := FindFiles(dir, "app.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %v", got)
	}
}

func TestFindFilesGlobQuery(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, dir, "a_test.go", "x")
	mkfile(t, dir, "a.go", "x")

	got, err := FindFiles(dir, "*_test.go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "a_test.go" {
		t.Fatalf("glob query failed: %v", got)
	}
}

func TestFindFilesRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		mkfile(t, dir, filepath.Join("p", "file"+string(rune('a'+i%26))+".go"), "x")
	}
	got, err := FindFiles(dir, ".go", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 5 {
		t.Fatalf("limit ignored: %d", len(got))
	}
}

func TestOutlineGo(t *testing.T) {
	dir := t.TempDir()
	src := `package main

import "fmt"

type Server struct{}

const Version = "1"

func (s *Server) Start() error { return nil }

func helper() {}

// func commentedOut() {}
`
	p := mkfile(t, dir, "srv.go", src)
	o, err := GetOutline(p)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[string]string{}
	for _, s := range o.Symbols {
		kinds[s.Name] = s.Kind
	}
	if kinds["Server"] != "type" {
		t.Fatalf("missing type Server: %+v", o.Symbols)
	}
	if kinds["Start"] != "func" {
		t.Fatalf("missing method Start: %+v", o.Symbols)
	}
	if kinds["helper"] != "func" {
		t.Fatalf("missing func helper: %+v", o.Symbols)
	}
	if _, bad := kinds["commentedOut"]; bad {
		t.Fatal("commented-out code must not be reported as a symbol")
	}
}

func TestOutlinePython(t *testing.T) {
	dir := t.TempDir()
	p := mkfile(t, dir, "m.py", "class Thing:\n    def method(self):\n        pass\n\nasync def main():\n    pass\n")
	o, err := GetOutline(p)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range o.Symbols {
		names[s.Name] = true
	}
	for _, want := range []string{"Thing", "method", "main"} {
		if !names[want] {
			t.Fatalf("missing %s: %+v", want, o.Symbols)
		}
	}
}

func TestOutlineMakefileTargets(t *testing.T) {
	dir := t.TempDir()
	p := mkfile(t, dir, "Makefile", ".PHONY: build\n\nbuild:\n\tgo build\n\nci: build test\n\tvar = notatarget\n")
	o, err := GetOutline(p)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range o.Symbols {
		names[s.Name] = true
	}
	if !names["build"] || !names["ci"] {
		t.Fatalf("expected build and ci targets: %+v", o.Symbols)
	}
}

func TestOutlineUnsupportedLanguage(t *testing.T) {
	dir := t.TempDir()
	p := mkfile(t, dir, "notes.txt", "hello")
	if _, err := GetOutline(p); err == nil {
		t.Fatal("expected an error for an unsupported file type")
	}
}

func TestOutlineRenderIncludesLineNumbers(t *testing.T) {
	dir := t.TempDir()
	p := mkfile(t, dir, "a.go", "package a\n\nfunc Alpha() {}\n")
	o, err := GetOutline(p)
	if err != nil {
		t.Fatal(err)
	}
	rendered := o.Render()
	if !strings.Contains(rendered, "Alpha") || !strings.Contains(rendered, "3") {
		t.Fatalf("render missing symbol or line: %q", rendered)
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"a.go":     "go",
		"a.py":     "python",
		"a.tsx":    "javascript",
		"A.java":   "java",
		"a.rs":     "rust",
		"Makefile": "make",
		"a.txt":    "",
	}
	for in, want := range cases {
		if got := DetectLanguage(in); got != want {
			t.Fatalf("%s: got %q want %q", in, got, want)
		}
	}
}
