package credentials

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// lookupTTL bounds how long a keychain result is reused.
//
// Reading the macOS keychain forks a `security` process. The adaptive router
// checks availability for every candidate profile on every turn, so an uncached
// lookup means several process spawns per message. A short TTL keeps that cheap
// while still noticing a key added from another terminal.
const lookupTTL = 30 * time.Second

type cacheEntry struct {
	value string
	err   error
	at    time.Time
}

type Manager struct {
	mu        sync.Mutex
	overrides map[string]string
	cache     map[string]cacheEntry
}

func New() *Manager {
	return &Manager{
		overrides: make(map[string]string),
		cache:     make(map[string]cacheEntry),
	}
}

func (m *Manager) Set(name, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overrides[name] = value
	// The new value must win immediately.
	delete(m.cache, name)
}

func (m *Manager) Get(name string) (string, error) {
	m.mu.Lock()
	if v, ok := m.overrides[name]; ok && v != "" {
		m.mu.Unlock()
		return v, nil
	}
	if e, ok := m.cache[name]; ok && time.Since(e.at) < lookupTTL {
		m.mu.Unlock()
		return e.value, e.err
	}
	m.mu.Unlock()

	// Resolve outside the lock: the keychain call is slow and must not block
	// concurrent lookups for other credentials.
	value, err := resolve(name)

	m.mu.Lock()
	m.cache[name] = cacheEntry{value: value, err: err, at: time.Now()}
	m.mu.Unlock()

	return value, err
}

// resolve reads a credential from the environment, then the keychain.
func resolve(name string) (string, error) {
	if v := os.Getenv(name); v != "" {
		return v, nil
	}
	if runtime.GOOS == "darwin" {
		return keychainGet(name)
	}
	return "", fmt.Errorf("credential not found: %s", name)
}

func (m *Manager) Has(name string) bool {
	_, err := m.Get(name)
	return err == nil
}

// Invalidate drops any cached result for name.
func (m *Manager) Invalidate(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, name)
}

func (m *Manager) Save(name, value string) error {
	m.Set(name, value)
	if runtime.GOOS == "darwin" {
		return keychainSet(name, value)
	}
	return fmt.Errorf("storing credentials is only supported on macOS; set %s in your environment", name)
}

func keychainSet(name, value string) error {
	// Remove any existing entry first to avoid duplicates.
	_ = keychainDelete(name)
	cmd := exec.Command("security", "add-generic-password", "-s", "moz", "-a", name, "-w", value, "-U")
	return cmd.Run()
}

func keychainGet(name string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", "moz", "-a", name, "-w")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("credential not found: %s", name)
	}
	return strings.TrimSpace(string(out)), nil
}

func keychainDelete(name string) error {
	cmd := exec.Command("security", "delete-generic-password", "-s", "moz", "-a", name)
	return cmd.Run()
}
