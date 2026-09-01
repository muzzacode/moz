package llm

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Retry defaults. Free provider tiers rate-limit aggressively, and losing an
// entire multi-turn task to a single 429 is unacceptable.
const (
	DefaultMaxRetries = 4
	baseBackoff       = 1 * time.Second
	maxBackoff        = 30 * time.Second
)

// retryableStatus lists HTTP statuses worth retrying: rate limits, request
// timeouts, and transient server-side failures.
func retryableStatus(code int) bool {
	switch code {
	case 408, 409, 425, 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

var statusRe = regexp.MustCompile(`status code: (\d{3})`)

// retryAfterRe captures a provider-supplied delay hint, in seconds.
var retryAfterRe = regexp.MustCompile(`(?i)retry[- ]after[":\s]+(\d+)`)

// classifyError decides whether err is worth retrying and how long to wait.
//
// The OpenAI SDK returns status codes inside an error string rather than a
// typed value for every provider, so the status is extracted textually and then
// combined with network-level checks.
func classifyError(err error) (retry bool, hint time.Duration) {
	if err == nil {
		return false, 0
	}
	// A cancelled parent context is a deliberate stop, never a transient fault.
	if errors.Is(err, context.Canceled) {
		return false, 0
	}

	msg := err.Error()

	if m := statusRe.FindStringSubmatch(msg); m != nil {
		code, convErr := strconv.Atoi(m[1])
		if convErr == nil && !retryableStatus(code) {
			return false, 0
		}
		if convErr == nil {
			return true, retryAfterHint(msg)
		}
	}

	// Deadline exceeded on an individual attempt is worth one more try; the
	// caller's own context governs whether there is time left.
	if errors.Is(err, context.DeadlineExceeded) {
		return true, 0
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true, 0
	}

	// Connection-level failures surfaced as opaque strings by HTTP clients.
	for _, s := range []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"unexpected EOF",
		"EOF",
		"no such host",
		"TLS handshake timeout",
		"server closed",
	} {
		if strings.Contains(msg, s) {
			return true, 0
		}
	}

	return false, 0
}

func retryAfterHint(msg string) time.Duration {
	if m := retryAfterRe.FindStringSubmatch(msg); m != nil {
		if secs, err := strconv.Atoi(m[1]); err == nil && secs > 0 && secs <= 120 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

// backoffFor returns the delay before the given attempt, using exponential
// growth with jitter. Jitter matters because a retry storm from several
// concurrent agents would otherwise stay synchronised.
func backoffFor(attempt int, hint time.Duration) time.Duration {
	if hint > 0 {
		return hint
	}
	d := time.Duration(float64(baseBackoff) * math.Pow(2, float64(attempt)))
	if d > maxBackoff {
		d = maxBackoff
	}
	// Full jitter over the lower half of the window.
	jitter := time.Duration(rand.Int63n(int64(d/2) + 1))
	return d/2 + jitter
}

// RetryNotice describes a retry attempt for display.
type RetryNotice struct {
	Attempt int
	Max     int
	Delay   time.Duration
	Err     error
}

// OnRetry is called before each retry so callers can surface progress.
type OnRetry func(RetryNotice)

// withRetry runs fn, retrying transient failures with exponential backoff.
func withRetry[T any](
	ctx context.Context,
	maxRetries int,
	notify OnRetry,
	fn func() (T, error),
) (T, error) {
	var zero T
	if maxRetries < 0 {
		maxRetries = 0
	}

	for attempt := 0; ; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		retry, hint := classifyError(err)
		if !retry || attempt >= maxRetries {
			return zero, err
		}

		delay := backoffFor(attempt, hint)
		if notify != nil {
			notify(RetryNotice{Attempt: attempt + 1, Max: maxRetries, Delay: delay, Err: err})
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
}
