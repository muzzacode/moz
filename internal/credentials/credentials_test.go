package credentials

import (
	"sync"
	"testing"
)

func TestOverrideWins(t *testing.T) {
	m := New()
	m.Set("MOZ_TEST_KEY", "from-override")

	got, err := m.Get("MOZ_TEST_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-override" {
		t.Fatalf("got %q", got)
	}
}

func TestEnvIsUsed(t *testing.T) {
	t.Setenv("MOZ_TEST_ENV_KEY", "from-env")

	m := New()
	got, err := m.Get("MOZ_TEST_ENV_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Fatalf("got %q", got)
	}
}

// A miss must be cached too. The router asks about every unavailable provider on
// every turn, and each uncached miss forks a `security` process on macOS.
func TestMissesAreCachedToAvoidRepeatedLookups(t *testing.T) {
	m := New()

	if m.Has("MOZ_DEFINITELY_ABSENT_KEY") {
		t.Fatal("key should be absent")
	}
	// Second call must be served from cache; correctness is the same either way,
	// so assert the cache entry exists rather than counting subprocesses.
	m.mu.Lock()
	_, cached := m.cache["MOZ_DEFINITELY_ABSENT_KEY"]
	m.mu.Unlock()
	if !cached {
		t.Fatal("a failed lookup should be cached")
	}
}

// Setting a value must take effect immediately, not after the cache expires.
func TestSetInvalidatesCachedMiss(t *testing.T) {
	m := New()

	if m.Has("MOZ_TEST_LATE_KEY") {
		t.Fatal("precondition: key should be absent")
	}

	m.Set("MOZ_TEST_LATE_KEY", "now-present")

	got, err := m.Get("MOZ_TEST_LATE_KEY")
	if err != nil {
		t.Fatalf("expected the new value to be visible immediately: %v", err)
	}
	if got != "now-present" {
		t.Fatalf("got %q", got)
	}
}

func TestInvalidateForcesReLookup(t *testing.T) {
	m := New()
	_ = m.Has("MOZ_TEST_INVALIDATE_KEY")

	m.Invalidate("MOZ_TEST_INVALIDATE_KEY")

	m.mu.Lock()
	_, cached := m.cache["MOZ_TEST_INVALIDATE_KEY"]
	m.mu.Unlock()
	if cached {
		t.Fatal("Invalidate should drop the cache entry")
	}
}

// The router may check several providers concurrently.
func TestConcurrentGetIsSafe(t *testing.T) {
	t.Setenv("MOZ_TEST_CONCURRENT", "value")
	m := New()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Get("MOZ_TEST_CONCURRENT")
			_ = m.Has("MOZ_TEST_ABSENT_CONCURRENT")
			m.Set("MOZ_TEST_WRITTEN", "x")
		}()
	}
	wg.Wait()

	if got, _ := m.Get("MOZ_TEST_CONCURRENT"); got != "value" {
		t.Fatalf("got %q", got)
	}
}

// An empty override must not shadow a real environment value.
func TestEmptyOverrideIsIgnored(t *testing.T) {
	t.Setenv("MOZ_TEST_EMPTY_OVERRIDE", "from-env")
	m := New()
	m.Set("MOZ_TEST_EMPTY_OVERRIDE", "")

	got, err := m.Get("MOZ_TEST_EMPTY_OVERRIDE")
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Fatalf("expected the env value, got %q", got)
	}
}
