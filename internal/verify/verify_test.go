package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectPrefersMakeCI(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "build:\n\tgo build\n\ntest:\n\tgo test\n\nci: vet test\n")
	writeFile(t, dir, "go.mod", "module x\n")

	cmd, ok := Detect(dir)
	if !ok {
		t.Fatal("expected detection")
	}
	if cmd.Shell != "make ci" {
		t.Fatalf("expected make ci, got %q", cmd.Shell)
	}
}

func TestDetectMakeBuildAndTest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "build:\n\tgo build\n\ntest:\n\tgo test\n")

	cmd, _ := Detect(dir)
	if cmd.Shell != "make build && make test" {
		t.Fatalf("got %q", cmd.Shell)
	}
}

// A phony declaration mentioning "ci" must not be mistaken for a ci target.
func TestDetectIgnoresPhonyMentions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", ".PHONY: build ci test\n\nbuild:\n\tgo build\n")

	cmd, ok := Detect(dir)
	if ok && strings.Contains(cmd.Shell, "make ci") {
		t.Fatalf("matched a .PHONY mention rather than a real target: %q", cmd.Shell)
	}
}

func TestDetectGoModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module x\n")

	cmd, ok := Detect(dir)
	if !ok || !strings.Contains(cmd.Shell, "go test") {
		t.Fatalf("expected a go command, got %q ok=%v", cmd.Shell, ok)
	}
}

func TestDetectNPMTestScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"x","scripts":{"build":"tsc","test":"vitest run"}}`)

	cmd, ok := Detect(dir)
	if !ok || !strings.Contains(cmd.Shell, "npm test") {
		t.Fatalf("expected npm test, got %q", cmd.Shell)
	}
}

// A package.json with no test script must fall through to build, not claim a
// test command that would fail immediately.
func TestDetectNPMWithoutTestFallsBackToBuild(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name":"x","scripts":{"build":"tsc"}}`)

	cmd, ok := Detect(dir)
	if !ok || !strings.Contains(cmd.Shell, "npm run build") {
		t.Fatalf("expected npm run build, got %q", cmd.Shell)
	}
}

func TestDetectUnknownProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.txt", "hello")

	if _, ok := Detect(dir); ok {
		t.Fatal("expected no detection for an unrecognised project")
	}
}

func TestRunSuccess(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), "exit 0", 30*time.Second)
	if !res.OK {
		t.Fatalf("expected success, got %+v", res)
	}
}

func TestRunFailureCapturesOutputAndExitCode(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), "echo boom >&2; exit 3", 30*time.Second)
	if res.OK {
		t.Fatal("expected failure")
	}
	if res.ExitCode != 3 {
		t.Fatalf("expected exit code 3, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "boom") {
		t.Fatalf("stderr not captured: %q", res.Stdout)
	}
	if fb := res.Feedback(); !strings.Contains(fb, "boom") || !strings.Contains(fb, "exit code 3") && !strings.Contains(fb, "Exit code: 3") {
		t.Fatalf("feedback missing detail: %q", fb)
	}
}

func TestRunTimesOut(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), "sleep 5", 200*time.Millisecond)
	if !res.TimedOut {
		t.Fatalf("expected timeout, got %+v", res)
	}
	if res.OK {
		t.Fatal("timed out run must not report success")
	}
	if !strings.Contains(res.Feedback(), "timed out") {
		t.Fatal("feedback should mention the timeout")
	}
}

func TestRunEmptyCommandSkips(t *testing.T) {
	res := Run(context.Background(), t.TempDir(), "   ", time.Second)
	if !res.Skipped {
		t.Fatal("expected skip for an empty command")
	}
}

func TestRunRespectsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Run(ctx, t.TempDir(), "sleep 5", 30*time.Second)
	if res.OK {
		t.Fatal("cancelled run must not report success")
	}
}

func TestClipKeepsTail(t *testing.T) {
	long := strings.Repeat("a", maxOutput*2) + "FAILURE_SUMMARY"
	got := clip(long)
	if !strings.Contains(got, "FAILURE_SUMMARY") {
		t.Fatal("clip must retain the tail where failures are reported")
	}
	if len(got) > maxOutput+100 {
		t.Fatalf("clip output too long: %d", len(got))
	}
}
