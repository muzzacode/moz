package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/muzzacode/moz/internal/config"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/verify"
)

// maxVerifyAttempts bounds the fix-and-recheck cycle. Without a cap, a model
// that cannot fix a failure would loop until the turn budget is exhausted.
const maxVerifyAttempts = 3

// verifyState tracks whether the workspace was modified and how many times
// verification has already been fed back to the model.
type verifyState struct {
	enabled  bool
	command  string
	source   string
	dir      string
	dirty    bool
	attempts int
}

func newVerifyState(cfg *config.Config) verifyState {
	if cfg == nil || !cfg.AgentOpts.Verify {
		return verifyState{}
	}

	// Always verify the directory the user is actually working in. Using a
	// configured workspace here would run another project's test suite.
	dir, err := os.Getwd()
	if err != nil {
		return verifyState{}
	}

	if cmd := cfg.AgentOpts.VerifyCommand; cmd != "" {
		return verifyState{enabled: true, command: cmd, source: "configured", dir: dir}
	}

	detected, ok := verify.Detect(dir)
	if !ok {
		return verifyState{}
	}
	return verifyState{enabled: true, command: detected.Shell, source: detected.Source, dir: dir}
}

func (v *verifyState) markDirty() { v.dirty = true }

// shouldRun reports whether a verification pass is warranted right now.
func (v *verifyState) shouldRun() bool {
	return v.enabled && v.dirty && v.attempts < maxVerifyAttempts
}

// runVerification runs the project's verification command when the agent is
// about to finish after editing files.
//
// It returns feedback and true when the model must keep working. It returns
// false when the agent may finalize, either because verification passed, was
// not applicable, or has already been retried too many times.
func (r *Runner) runVerification(
	ctx context.Context,
	v *verifyState,
	out chan<- Event,
	start time.Time,
	profile *models.Profile,
) (string, bool) {
	if !v.shouldRun() {
		if v.enabled && v.dirty && v.attempts >= maxVerifyAttempts {
			out <- Event{
				Type:    "warning",
				Step:    fmt.Sprintf("verification still failing after %d attempts; reporting anyway", maxVerifyAttempts),
				Elapsed: time.Since(start),
			}
		}
		return "", false
	}
	if ctx.Err() != nil {
		return "", false
	}

	v.attempts++
	out <- Event{
		Type:    "step",
		Step:    fmt.Sprintf("verifying (%s): %s", v.source, v.command),
		Model:   profile.Name,
		Elapsed: time.Since(start),
	}

	res := verify.Run(ctx, v.dir, v.command, verify.DefaultTimeout)
	if res.Skipped {
		return "", false
	}
	if ctx.Err() != nil {
		return "", false
	}

	if res.OK {
		// Passing clears the dirty flag so later edits re-trigger a check.
		v.dirty = false
		out <- Event{Type: "verified", Step: "verification passed", Elapsed: time.Since(start)}
		return "", false
	}

	out <- Event{
		Type:    "warning",
		Step:    fmt.Sprintf("verification failed (exit %d); asking the model to fix it", res.ExitCode),
		Elapsed: time.Since(start),
	}
	return res.Feedback(), true
}

// mutatesFiles reports whether a tool can change the workspace. exec is
// included because the model routinely uses it for generators and formatters.
func mutatesFiles(tool string) bool {
	switch tool {
	case "write_file", "edit_file", "exec":
		return true
	default:
		return false
	}
}
