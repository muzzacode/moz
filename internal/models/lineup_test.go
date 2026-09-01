package models

import "testing"

// Every profile referenced by a stack must exist, or routing silently skips it
// and the intended model is never used.
func TestStackProfilesAllExist(t *testing.T) {
	r := DefaultProfiles()
	ids := map[string]bool{}
	for _, p := range r.Profiles {
		ids[p.ID] = true
	}
	for _, s := range r.Stacks {
		if len(s.Profiles) == 0 {
			t.Fatalf("stack %q is empty", s.Name)
		}
		for _, id := range s.Profiles {
			if !ids[id] {
				t.Fatalf("stack %q references unknown profile %q", s.Name, id)
			}
		}
	}
}

// Every task class needs a stack, otherwise routing falls back to chat.
func TestEveryTaskClassHasAStack(t *testing.T) {
	r := DefaultProfiles()
	have := map[TaskClass]bool{}
	for _, s := range r.Stacks {
		have[s.Class] = true
	}
	for _, c := range []TaskClass{
		TaskQuickChat, TaskChat, TaskCodeEdit, TaskDebug,
		TaskReasoning, TaskArchitecture, TaskVision,
	} {
		if !have[c] {
			t.Fatalf("no stack for task class %q", c)
		}
	}
}

// Stacks are documented as cheapest first. Routing does not depend on it, but a
// mis-ordered stack is misleading and affects tiebreaks within a tier.
func TestStacksAreOrderedCheapestFirst(t *testing.T) {
	rank := map[string]int{"local": 0, "cloud-cheap": 1, "cloud-premium": 2}
	r := DefaultProfiles()

	byID := map[string]*Profile{}
	for i := range r.Profiles {
		byID[r.Profiles[i].ID] = &r.Profiles[i]
	}

	for _, s := range r.Stacks {
		prev := -1
		for _, id := range s.Profiles {
			got, ok := rank[byID[id].CostTier]
			if !ok {
				t.Fatalf("profile %q has an unrecognised cost_tier %q", id, byID[id].CostTier)
			}
			if got < prev {
				t.Fatalf("stack %q is not ordered cheapest first at %q", s.Name, id)
			}
			prev = got
		}
	}
}

// A stack that can reach a paid model must offer a free option first, so
// ordinary work never has to cost money.
func TestCodingStacksStartLocal(t *testing.T) {
	r := DefaultProfiles()
	byID := map[string]*Profile{}
	for i := range r.Profiles {
		byID[r.Profiles[i].ID] = &r.Profiles[i]
	}

	for _, s := range r.Stacks {
		// Vision is exempt: no local vision model is installed.
		if s.Class == TaskVision {
			continue
		}
		if byID[s.Profiles[0]].CostTier != "local" {
			t.Fatalf("stack %q should start with a local model, starts with %q", s.Name, s.Profiles[0])
		}
	}
}

// The vision stack must only contain models that can actually accept images.
// Claiming vision on a text-only model produces confident nonsense.
func TestVisionStackModelsSupportVision(t *testing.T) {
	r := DefaultProfiles()
	byID := map[string]*Profile{}
	for i := range r.Profiles {
		byID[r.Profiles[i].ID] = &r.Profiles[i]
	}

	for _, s := range r.Stacks {
		if s.Class != TaskVision {
			continue
		}
		for _, id := range s.Profiles {
			if !byID[id].Has(CapVision) {
				t.Fatalf("vision stack includes %q which does not support vision", id)
			}
		}
	}
}

// Anything that can be routed to must declare tool calling, since the agent
// loop depends on it.
func TestStackedProfilesSupportToolCalling(t *testing.T) {
	r := DefaultProfiles()
	byID := map[string]*Profile{}
	for i := range r.Profiles {
		byID[r.Profiles[i].ID] = &r.Profiles[i]
	}
	for _, s := range r.Stacks {
		for _, id := range s.Profiles {
			if !byID[id].Has(CapToolCalling) {
				t.Fatalf("stack %q includes %q which cannot call tools", s.Name, id)
			}
		}
	}
}

// Paid profiles need a credential name, or they would appear free and always be
// considered available.
func TestPaidProfilesDeclareACredential(t *testing.T) {
	for _, p := range DefaultProfiles().Profiles {
		if p.CostTier == "local" {
			continue
		}
		if p.APIKeyCredential == "" {
			t.Fatalf("paid profile %q declares no API key credential", p.ID)
		}
	}
}

func TestProfileIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range DefaultProfiles().Profiles {
		if seen[p.ID] {
			t.Fatalf("duplicate profile ID %q", p.ID)
		}
		seen[p.ID] = true
	}
}
