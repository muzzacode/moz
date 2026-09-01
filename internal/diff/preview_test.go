package diff

import (
	"strings"
	"testing"
)

const makefile = ".PHONY: build test\n\nbuild:\n\tgo build\n\ntest:\n\tgo test\n\nhelp:\n\t@echo help\n"

func TestPreviewEditShowsRemovedAndAddedLines(t *testing.T) {
	got := PreviewEdit("Makefile", makefile, "help:\n\t@echo help", "ci: build test", false)

	if !strings.Contains(got, "- help:") {
		t.Fatalf("removed line not shown:\n%s", got)
	}
	if !strings.Contains(got, "+ ci: build test") {
		t.Fatalf("added line not shown:\n%s", got)
	}
	if !strings.Contains(got, "edit Makefile") {
		t.Fatalf("missing header:\n%s", got)
	}
}

// The dangerous case: a non-unique old_string would silently clobber content.
// The preview must say so before the user approves.
func TestPreviewEditWarnsOnAmbiguousMatch(t *testing.T) {
	got := PreviewEdit("Makefile", makefile, "go", "golang", false)
	if !strings.Contains(got, "matches") || !strings.Contains(got, "replace_all") {
		t.Fatalf("expected an ambiguity warning:\n%s", got)
	}
}

func TestPreviewEditReportsMissingOldString(t *testing.T) {
	got := PreviewEdit("Makefile", makefile, "nonexistent", "x", false)
	if !strings.Contains(got, "not found") {
		t.Fatalf("expected a not-found warning:\n%s", got)
	}
}

func TestPreviewEditRejectsEmptyOldString(t *testing.T) {
	got := PreviewEdit("Makefile", makefile, "", "x", false)
	if !strings.Contains(got, "invalid") {
		t.Fatalf("expected an invalid marker:\n%s", got)
	}
}

func TestPreviewEditReportsOccurrenceCountForReplaceAll(t *testing.T) {
	got := PreviewEdit("Makefile", makefile, "go", "golang", true)

	if !strings.Contains(got, "occurrences") {
		t.Fatalf("expected an occurrence count:\n%s", got)
	}
}

func TestPreviewEditIncludesLineNumbers(t *testing.T) {
	got := PreviewEdit("Makefile", makefile, "test:\n\tgo test", "test:\n\tgo test -race", false)
	// "build:" is on line 3 and should appear as leading context.
	if !strings.Contains(got, "   3   build:") {
		t.Fatalf("expected numbered context lines:\n%s", got)
	}
}

func TestPreviewEditTruncatesHugeChanges(t *testing.T) {
	big := strings.Repeat("x\n", 500)
	got := PreviewEdit("f.txt", "a\n", "a\n", big, false)
	if !strings.Contains(got, "more lines") {
		t.Fatalf("expected truncation marker:\n%s", got)
	}
	if n := strings.Count(got, "\n"); n > maxPreviewLines+20 {
		t.Fatalf("preview too long: %d lines", n)
	}
}

func TestPreviewWriteShowsContent(t *testing.T) {
	got := PreviewWrite("new.go", "package main\n\nfunc main() {}\n")
	if !strings.Contains(got, "create new.go") {
		t.Fatalf("missing header:\n%s", got)
	}
	if !strings.Contains(got, "+ package main") {
		t.Fatalf("missing content:\n%s", got)
	}
}

func TestPreviewEditAtStartOfFile(t *testing.T) {
	// Must not panic when there is no leading context available.
	got := PreviewEdit("f.txt", "first\nsecond\n", "first", "1st", false)
	if !strings.Contains(got, "- first") {
		t.Fatalf("unexpected preview:\n%s", got)
	}
}

func TestPreviewEditAtEndOfFile(t *testing.T) {
	got := PreviewEdit("f.txt", "a\nb\nlast", "last", "final", false)
	if !strings.Contains(got, "+ final") {
		t.Fatalf("unexpected preview:\n%s", got)
	}
}
