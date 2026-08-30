// Package verify detects and runs a project's own verification command so the
// agent can check its work instead of assuming an edit was correct.
package verify

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultTimeout bounds a verification run. Test suites hang; the agent must not.
const DefaultTimeout = 5 * time.Minute

// maxOutput caps captured output so a noisy suite cannot blow the context window.
const maxOutput = 6000

type Command struct {
	// Shell is the command line to execute.
	Shell string
	// Source describes how the command was chosen, for display.
	Source string
}

type Result struct {
	Command  string
	Stdout   string
	ExitCode int
	OK       bool
	TimedOut bool
	Skipped  bool
}

// makeTarget matches a target definition at the start of a line, e.g. "ci:".
func makeTarget(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*:`)
}

// Detect picks a verification command for dir.
//
// Preference order favours the project's own aggregate entry point, because a
// repo that defines "make ci" has already declared what "correct" means.
func Detect(dir string) (Command, bool) {
	if mk, ok := readFirst(dir, "Makefile", "makefile", "GNUmakefile"); ok {
		for _, target := range []string{"ci", "check", "verify"} {
			if makeTarget(target).MatchString(mk) {
				return Command{Shell: "make " + target, Source: "Makefile target " + target}, true
			}
		}
		hasTest := makeTarget("test").MatchString(mk)
		hasBuild := makeTarget("build").MatchString(mk)
		switch {
		case hasBuild && hasTest:
			return Command{Shell: "make build && make test", Source: "Makefile build+test"}, true
		case hasTest:
			return Command{Shell: "make test", Source: "Makefile target test"}, true
		}
	}

	if exists(dir, "go.mod") {
		return Command{Shell: "go build ./... && go test ./...", Source: "go.mod"}, true
	}
	if exists(dir, "Cargo.toml") {
		return Command{Shell: "cargo test", Source: "Cargo.toml"}, true
	}
	if pkg, ok := readFirst(dir, "package.json"); ok {
		if hasNPMScript(pkg, "test") {
			return Command{Shell: "npm test --silent", Source: "package.json test script"}, true
		}
		if hasNPMScript(pkg, "build") {
			return Command{Shell: "npm run build --silent", Source: "package.json build script"}, true
		}
	}
	if exists(dir, "pyproject.toml") || exists(dir, "setup.py") || exists(dir, "pytest.ini") {
		return Command{Shell: "pytest -q", Source: "python project"}, true
	}

	return Command{}, false
}

// Run executes cmd in dir and reports whether verification passed.
func Run(ctx context.Context, dir, shellCmd string, timeout time.Duration) Result {
	if strings.TrimSpace(shellCmd) == "" {
		return Result{Skipped: true}
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", shellCmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	res := Result{Command: shellCmd, Stdout: clip(string(out))}

	switch {
	case err == nil:
		res.OK = true
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		res.ExitCode = -1
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
			if res.Stdout == "" {
				res.Stdout = err.Error()
			}
		}
	}
	return res
}

// Feedback renders a failed result as instructions for the model.
func (r Result) Feedback() string {
	var b strings.Builder
	b.WriteString("Verification failed. Fix the problem before answering.\n")
	b.WriteString("Command: " + r.Command + "\n")
	if r.TimedOut {
		b.WriteString("Result: timed out\n")
	} else {
		b.WriteString("Exit code: " + itoa(r.ExitCode) + "\n")
	}
	if r.Stdout != "" {
		b.WriteString("Output:\n")
		b.WriteString(r.Stdout)
	}
	return b.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// clip keeps the tail of the output, where compilers and test runners put the
// failure summary.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxOutput {
		return s
	}
	return "... (earlier output omitted) ...\n" + s[len(s)-maxOutput:]
}

func exists(dir, name string) bool {
	st, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !st.IsDir()
}

func readFirst(dir string, names ...string) (string, bool) {
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err == nil {
			return string(data), true
		}
	}
	return "", false
}

// hasNPMScript reports whether package.json declares the named script. It uses
// a targeted match rather than full JSON parsing so a malformed or exotic
// manifest cannot break detection.
func hasNPMScript(pkg, name string) bool {
	idx := strings.Index(pkg, `"scripts"`)
	if idx < 0 {
		return false
	}
	return regexp.MustCompile(`"` + regexp.QuoteMeta(name) + `"\s*:`).MatchString(pkg[idx:])
}
