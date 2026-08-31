package adaptive

import (
	"github.com/muzzacode/moz/internal/models"
)

// Escalate finds a more capable model to retry a failed task with.
//
// This is where local-first actually pays off. Without it, a weak local model
// that produces malformed tool calls or loops until its turn limit simply fails
// the task, and the user reaches for a frontier model manually for everything.
// With it, local handles the bulk of the work and cloud rescues the remainder.
//
// It returns nil when there is nothing better available, which includes the case
// where the budget is spent.
func (rt *Router) Escalate(from *models.Profile, class models.TaskClass) *models.Profile {
	if from == nil {
		return nil
	}
	// A frontier model failing is not something a different model will fix
	// cheaply, and retrying it would double the most expensive call.
	if tierOf(from) == tierPremium {
		return nil
	}
	if rt.Budget != nil && rt.Budget.Exhausted() {
		return nil
	}

	stack, ok := rt.Registry.FindStack(class)
	if !ok {
		stack, ok = rt.Registry.FindStack(models.TaskChat)
		if !ok {
			return nil
		}
	}

	fromRank := tierRank(tierOf(from))

	var best *models.Profile
	bestRank := 99

	for _, id := range stack.Profiles {
		p, err := rt.Registry.Find(id)
		if err != nil || p.ID == from.ID {
			continue
		}
		if !rt.isAvailable(p) {
			continue
		}
		rank := tierRank(tierOf(p))
		// Strictly more expensive than what just failed, and the cheapest such
		// option rather than the most capable.
		if rank > fromRank && rank < bestRank {
			best, bestRank = p, rank
		}
	}
	return best
}

// Fallback finds a usable model after one has failed outright.
//
// Escalation only moves up a tier, which cannot help when the failure is the
// provider itself: an expired key, an empty credit balance, or a region outage.
//
// It degrades one step at a time rather than dropping to the cheapest option. A
// task that was judged to need a frontier model is still a hard task, so the
// closest tier below the failure is a better answer than the cheapest model in
// the stack.
func (rt *Router) Fallback(from *models.Profile, class models.TaskClass) *models.Profile {
	if from == nil {
		return nil
	}

	stack, ok := rt.Registry.FindStack(class)
	if !ok {
		stack, ok = rt.Registry.FindStack(models.TaskChat)
		if !ok {
			return nil
		}
	}

	budgetSpent := rt.Budget != nil && rt.Budget.Exhausted()
	fromRank := tierRank(tierOf(from))

	var best *models.Profile
	bestRank := -1

	for _, id := range stack.Profiles {
		p, err := rt.Registry.Find(id)
		if err != nil || p.ID == from.ID {
			continue
		}
		if !rt.isAvailable(p) {
			continue
		}
		rank := tierRank(tierOf(p))
		// Strictly cheaper than what failed, since anything at or above it is
		// escalation's job.
		if rank >= fromRank {
			continue
		}
		// Once the ceiling is reached, only free inference remains acceptable.
		if budgetSpent && rank > tierRank(tierLocal) {
			continue
		}
		// Highest tier below the failure: degrade gradually.
		if rank > bestRank {
			best, bestRank = p, rank
		}
	}
	return best
}

func tierRank(tier string) int {
	switch tier {
	case tierLocal:
		return 0
	case tierCheap:
		return 1
	case tierPremium:
		return 2
	default:
		return 1
	}
}
