package credentials

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Manager struct {
	overrides map[string]string
}

func New() *Manager {
	return &Manager{overrides: make(map[string]string)}
}

func (m *Manager) Set(name, value string) {
	m.overrides[name] = value
}

func (m *Manager) Get(name string) (string, error) {
	if v, ok := m.overrides[name]; ok && v != "" {
		return v, nil
	}
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
