package adaptive

import (
	"fmt"

	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/models"
)

type Decision struct {
	Profile *models.Profile
	Class   models.TaskClass
	Reason  string
	Params  map[string]any
}

type Router struct {
	Registry    *models.Registry
	Credentials *credentials.Manager
	PreferLocal bool
}

func New(r *models.Registry, cm *credentials.Manager) *Router {
	if cm == nil {
		cm = credentials.New()
	}
	return &Router{Registry: r, Credentials: cm, PreferLocal: true}
}

func (rt *Router) Select(prompt string) (*Decision, error) {
	signal := Classify(prompt)

	stack, ok := rt.Registry.FindStack(signal.Class)
	if !ok {
		stack, _ = rt.Registry.FindStack(models.TaskChat)
	}

	var selected *models.Profile
	var reason string

	for _, id := range stack.Profiles {
		p, err := rt.Registry.Find(id)
		if err != nil {
			continue
		}
		if !rt.isAvailable(p) {
			continue
		}

		// If we already found a local model and prefer local, only promote to cloud if the task strongly demands it.
		if selected != nil && rt.PreferLocal && selected.IsLocal() && !p.IsLocal() {
			if signal.Boost < 0.75 {
				continue
			}
		}

		selected = p
		reason = fmt.Sprintf("%s (class: %s, stack: %s)", signal.Reason, signal.Class, stack.Name)
		if !p.IsLocal() {
			break // pick first available cloud model in the stack
		}
	}

	if selected == nil {
		return nil, fmt.Errorf("no available model for task class %s", signal.Class)
	}

	return &Decision{
		Profile: selected,
		Class:   signal.Class,
		Reason:  reason,
		Params:  selected.DefaultParams,
	}, nil
}

func (rt *Router) isAvailable(p *models.Profile) bool {
	if p.ProviderKind == models.ProviderGoogle {
		// Not implemented yet; avoid false availability.
		return false
	}
	if p.IsLocal() {
		return true
	}
	if p.APIKeyCredential != "" {
		return rt.Credentials.Has(p.APIKeyCredential)
	}
	return false
}

func (rt *Router) Available(p *models.Profile) bool {
	return rt.isAvailable(p)
}
