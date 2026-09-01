package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassifyRetriesRateLimit(t *testing.T) {
	err := errors.New(`error, status code: 429, status: 429 Too Many Requests, message: Provider returned error`)
	retry, _ := classifyError(err)
	if !retry {
		t.Fatal("429 must be retried")
	}
}

func TestClassifyRetriesServerErrors(t *testing.T) {
	for _, code := range []int{500, 502, 503, 504, 529} {
		err := fmt.Errorf("error, status code: %d, status: boom", code)
		if retry, _ := classifyError(err); !retry {
			t.Fatalf("%d must be retried", code)
		}
	}
}

// Client errors are the model's or our fault and will never succeed on retry.
func TestClassifyDoesNotRetryClientErrors(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 422} {
		err := fmt.Errorf("error, status code: %d, status: nope", code)
		if retry, _ := classifyError(err); retry {
			t.Fatalf("%d must not be retried", code)
		}
	}
}

func TestClassifyDoesNotRetryCancellation(t *testing.T) {
	if retry, _ := classifyError(context.Canceled); retry {
		t.Fatal("cancellation is deliberate and must not be retried")
	}
	if retry, _ := classifyError(fmt.Errorf("wrapped: %w", context.Canceled)); retry {
		t.Fatal("wrapped cancellation must not be retried")
	}
}

func TestClassifyRetriesNetworkFailures(t *testing.T) {
	for _, msg := range []string{
		"connection reset by peer",
		"connection refused",
		"unexpected EOF",
		"dial tcp: lookup api.example.com: no such host",
		"net/http: TLS handshake timeout",
	} {
		if retry, _ := classifyError(errors.New(msg)); !retry {
			t.Fatalf("%q must be retried", msg)
		}
	}
}

func TestClassifyHonoursRetryAfterHint(t *testing.T) {
	err := errors.New(`status code: 429, message: rate limited, retry-after: 7`)
	retry, hint := classifyError(err)
	if !retry {
		t.Fatal("expected retry")
	}
	if hint != 7*time.Second {
		t.Fatalf("expected a 7s hint, got %s", hint)
	}
}

// An absurd hint must not be honoured, or a bad provider could stall the agent.
func TestClassifyIgnoresUnreasonableRetryAfter(t *testing.T) {
	_, hint := classifyError(errors.New("status code: 429, retry-after: 9999"))
	if hint != 0 {
		t.Fatalf("expected the hint to be ignored, got %s", hint)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	prevMax := time.Duration(0)
	for attempt := 0; attempt < 8; attempt++ {
		d := backoffFor(attempt, 0)
		if d <= 0 {
			t.Fatalf("attempt %d produced a non-positive delay", attempt)
		}
		if d > maxBackoff {
			t.Fatalf("attempt %d exceeded the cap: %s", attempt, d)
		}
		if d > prevMax {
			prevMax = d
		}
	}
	if prevMax <= baseBackoff/2 {
		t.Fatalf("backoff never grew beyond %s", prevMax)
	}
}

func TestBackoffPrefersProviderHint(t *testing.T) {
	if got := backoffFor(0, 5*time.Second); got != 5*time.Second {
		t.Fatalf("expected the hint to win, got %s", got)
	}
}

func TestWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	calls := 0
	got, err := withRetry(context.Background(), 4, nil, func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("status code: 429, status: rate limited")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" || calls != 3 {
		t.Fatalf("got %q after %d calls", got, calls)
	}
}

func TestWithRetryGivesUpOnPermanentError(t *testing.T) {
	calls := 0
	_, err := withRetry(context.Background(), 4, nil, func() (string, error) {
		calls++
		return "", errors.New("status code: 400, status: bad request")
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("permanent errors must not be retried, got %d calls", calls)
	}
}

func TestWithRetryRespectsMaxRetries(t *testing.T) {
	calls := 0
	_, err := withRetry(context.Background(), 2, nil, func() (string, error) {
		calls++
		return "", errors.New("status code: 503, status: unavailable")
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	// One initial attempt plus two retries.
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestWithRetryStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := withRetry(ctx, 10, nil, func() (string, error) {
		calls++
		return "", errors.New("status code: 429, status: rate limited")
	})
	if err == nil {
		t.Fatal("expected cancellation to surface")
	}
	if calls > 3 {
		t.Fatalf("cancellation should stop retries promptly, got %d calls", calls)
	}
}

func TestWithRetryNotifies(t *testing.T) {
	var notices []RetryNotice
	_, _ = withRetry(context.Background(), 2, func(n RetryNotice) {
		notices = append(notices, n)
	}, func() (string, error) {
		return "", errors.New("status code: 429, status: rate limited")
	})
	if len(notices) != 2 {
		t.Fatalf("expected 2 notices, got %d", len(notices))
	}
	if notices[0].Attempt != 1 || notices[0].Max != 2 {
		t.Fatalf("unexpected notice: %+v", notices[0])
	}
}
