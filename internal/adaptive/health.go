package adaptive

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Health probe tuning.
//
// The probe runs during model selection, so it must be fast: a slow check would
// add latency to every message. Results are cached, with failures re-checked
// sooner than successes so that starting Ollama mid-session is noticed quickly.
const (
	probeTimeout = 800 * time.Millisecond
	healthyTTL   = 60 * time.Second
	unhealthyTTL = 5 * time.Second
)

type healthResult struct {
	up bool
	at time.Time
}

// HealthChecker reports whether a local model server is reachable.
//
// Without this a local profile is always considered available, so adaptive mode
// happily routes to Ollama when it is not running, producing a failed turn
// instead of falling back to a cloud model.
type HealthChecker struct {
	mu     sync.Mutex
	cache  map[string]healthResult
	client *http.Client
	// Probe is overridable for tests.
	Probe func(ctx context.Context, baseURL string) bool
}

func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		cache:  make(map[string]healthResult),
		client: &http.Client{Timeout: probeTimeout},
	}
}

// Up reports whether the server behind baseURL is reachable.
func (h *HealthChecker) Up(baseURL string) bool {
	if baseURL == "" {
		return false
	}

	h.mu.Lock()
	if r, ok := h.cache[baseURL]; ok {
		ttl := healthyTTL
		if !r.up {
			ttl = unhealthyTTL
		}
		if time.Since(r.at) < ttl {
			h.mu.Unlock()
			return r.up
		}
	}
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	probe := h.Probe
	if probe == nil {
		probe = h.httpProbe
	}
	up := probe(ctx, baseURL)

	h.mu.Lock()
	h.cache[baseURL] = healthResult{up: up, at: time.Now()}
	h.mu.Unlock()

	return up
}

// httpProbe asks the server for its model list.
//
// Any HTTP response proves the server is listening, which is all we need. Only a
// transport error counts as down, so an unexpected status code does not make a
// running server look dead.
func (h *HealthChecker) httpProbe(ctx context.Context, baseURL string) bool {
	url := strings.TrimSuffix(baseURL, "/") + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return true
}

// Invalidate forgets the cached state for baseURL.
func (h *HealthChecker) Invalidate(baseURL string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.cache, baseURL)
}
