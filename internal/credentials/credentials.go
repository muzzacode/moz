package credentials

import (
	"fmt"
	"os"
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
	return "", fmt.Errorf("credential not found: %s", name)
}

func (m *Manager) Has(name string) bool {
	_, err := m.Get(name)
	return err == nil
}
