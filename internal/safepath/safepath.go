package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Policy struct {
	Allowed []string
}

func New(allowed []string) *Policy {
	return &Policy{Allowed: allowed}
}

func (p *Policy) Resolve(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("path is empty")
	}

	// Expand ~ to home.
	home, err := os.UserHomeDir()
	if err == nil && len(input) >= 2 && input[:2] == "~/" {
		input = filepath.Join(home, input[2:])
	}

	// Make absolute and clean.
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)

	// If no explicit allow list, allow the current working directory and home.
	allowed := p.Allowed
	if len(allowed) == 0 {
		cwd, _ := os.Getwd()
		allowed = []string{cwd, home}
	}

	for _, root := range allowed {
		root, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		root = filepath.Clean(root)

		if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return abs, nil
		}
	}

	return "", fmt.Errorf("path %q is outside allowed workspace", abs)
}
