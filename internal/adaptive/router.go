package adaptive

import (
	"fmt"
	"strings"

	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/models"
)

// Cost tiers, cheapest first. Routing walks this order so the cheapest capable
// model wins and a frontier model is only reached when the task justifies it.
const (
	tierLocal   = "local"
	tierCheap   = "cloud-cheap"
	tierPremium = "cloud-premium"
	tierUnknown = "unknown"
)

// Default promotion thresholds, expressed as classifier confidence.
//
// The classifier scores how demanding a task looks. Escalating to paid inference
// requires clearing cloudThreshold, and reaching a frontier model requires the
// higher premiumThreshold.
const (
	DefaultCloudThreshold   = 0.5
	DefaultPremiumThreshold = 0.8
)

type Decision struct {
	Profile *models.Profile
	Class   models.TaskClass
	Reason  string
	Params  map[string]any
	// Tier is the selected profile's cost tier.
	Tier string
	// Downgraded reports that a cheaper model was chosen than the task warranted,
	// because of the budget ceiling.
	Downgraded bool
}

type Router struct {
	Registry    *models.Registry
	Credentials *credentials.Manager
	PreferLocal bool

	// CloudThreshold is the confidence needed to leave local inference.
	CloudThreshold float64
	// PremiumThreshold is the confidence needed to use a frontier model.
	PremiumThreshold float64
	// Budget, when set, caps session spend and forces cheaper routing once hit.
	Budget *Budget
	// Health reports whether a local server is reachable.
	Health *HealthChecker
}

func New(r *models.Registry, cm *credentials.Manager) *Router {
	if cm == nil {
		cm = credentials.New()
	}
	return &Router{
		Registry:         r,
		Credentials:      cm,
		PreferLocal:      true,
		CloudThreshold:   DefaultCloudThreshold,
		PremiumThreshold: DefaultPremiumThreshold,
		Health:           NewHealthChecker(),
	}
}

// Options carries the routing settings drawn from configuration.
//
// Defined here rather than importing config so this package stays a leaf and can
// be tested without configuration plumbing.
type Options struct {
	PreferLocal      bool
	CloudThreshold   float64
	PremiumThreshold float64
	MaxSessionCost   float64
}

// NewWithOptions builds a router that honours the supplied settings and shares a
// budget across the session.
//
// Every caller should use this, so routing behaviour cannot drift between the
// TUI, the agent, and headless runs.
func NewWithOptions(r *models.Registry, cm *credentials.Manager, opts Options, budget *Budget) *Router {
	rt := New(r, cm)
	rt.PreferLocal = opts.PreferLocal
	rt.CloudThreshold = opts.CloudThreshold
	rt.PremiumThreshold = opts.PremiumThreshold
	if budget == nil {
		budget = NewBudget(opts.MaxSessionCost)
	}
	rt.Budget = budget
	return rt
}

func (rt *Router) cloudThreshold() float64 {
	if rt.CloudThreshold <= 0 {
		return DefaultCloudThreshold
	}
	return rt.CloudThreshold
}

func (rt *Router) premiumThreshold() float64 {
	if rt.PremiumThreshold <= 0 {
		return DefaultPremiumThreshold
	}
	return rt.PremiumThreshold
}

// Select chooses a model for a prompt.
//
// Candidates come from the task class's stack, already ordered cheapest first.
// A candidate is skipped when it is unavailable, when the task does not justify
// its cost tier, or when the budget will not allow it.
func (rt *Router) Select(prompt string) (*Decision, error) {
	signal := Classify(prompt)

	stack, ok := rt.Registry.FindStack(signal.Class)
	if !ok {
		stack, ok = rt.Registry.FindStack(models.TaskChat)
		if !ok {
			return nil, fmt.Errorf("no stack configured for task class %s", signal.Class)
		}
	}

	required, why, capped := rt.requiredTier(signal.Boost)

	// Pick the cheapest available model that meets the requirement.
	//
	// Cost preference is derived from the tier rather than from stack ordering,
	// so a mis-ordered stack or a mislabelled profile cannot make a paid model
	// win over a free one. Stack position is only a tiebreaker within a tier.
	var (
		chosen     *models.Profile
		chosenTier string
		chosenRank = 99
	)
	for _, id := range stack.Profiles {
		p, err := rt.Registry.Find(id)
		if err != nil || !rt.isAvailable(p) {
			continue
		}
		tier := tierOf(p)
		rank := tierRank(tier)
		if rank < tierRank(required) {
			continue
		}
		if rank < chosenRank {
			chosen, chosenTier, chosenRank = p, tier, rank
		}
	}

	if chosen != nil {
		return &Decision{
			Profile: chosen,
			Class:   signal.Class,
			Tier:    chosenTier,
			Reason:  fmt.Sprintf("%s (class: %s, stack: %s, tier: %s) — %s", signal.Reason, signal.Class, stack.Name, chosenTier, why),
			Params:  chosen.DefaultParams,
			// A budget-forced choice is a downgrade even though it matched, so
			// the UI can say why a cheaper model is being used.
			Downgraded: capped,
		}, nil
	}

	// Nothing at the required level is usable. Run on the cheapest available
	// model rather than failing, and say so.
	chosen, chosenTier = rt.cheapestAvailable(stack)
	if chosen == nil {
		return nil, fmt.Errorf("no available model for task class %s (check that Ollama is running or an API key is set)", signal.Class)
	}
	return &Decision{
		Profile:    chosen,
		Class:      signal.Class,
		Tier:       chosenTier,
		Reason:     fmt.Sprintf("%s (class: %s, stack: %s) — wanted %s but using %s: no %s model available", signal.Reason, signal.Class, stack.Name, required, chosen.Name, required),
		Params:     chosen.DefaultParams,
		Downgraded: true,
	}, nil
}

// requiredTier maps task difficulty to the minimum cost tier worth using, and
// explains the choice.
//
// The tier is a floor, not a ceiling: a cheap model handling a hard task badly
// costs more in wasted turns than routing it correctly the first time. A spent
// budget overrides the floor, since not spending is the whole point of a ceiling.
// It returns the tier, an explanation, and whether the budget forced the choice.
func (rt *Router) requiredTier(boost float64) (string, string, bool) {
	if rt.Budget != nil && rt.Budget.Exhausted() {
		return tierLocal, fmt.Sprintf("session budget of $%.2f is spent, staying on the cheapest option", rt.Budget.Limit()), true
	}

	cloud := rt.cloudThreshold()
	// A frontier model can never be reachable below the cloud bar, so keep the
	// thresholds consistent even if configured otherwise.
	premium := rt.premiumThreshold()
	if premium < cloud {
		premium = cloud
	}

	switch {
	case boost >= premium:
		return tierPremium, fmt.Sprintf("confidence %.2f meets the frontier bar %.2f", boost, premium), false

	case boost >= cloud:
		if rt.PreferLocal {
			// Bias toward free inference and rely on escalation if it struggles.
			return tierLocal, fmt.Sprintf("confidence %.2f warrants cloud, but prefer_local keeps it local first", boost), false
		}
		return tierCheap, fmt.Sprintf("confidence %.2f meets the cloud bar %.2f", boost, cloud), false

	default:
		return tierLocal, fmt.Sprintf("confidence %.2f is below the cloud bar %.2f", boost, cloud), false
	}
}

// cheapestAvailable returns the least expensive usable model in the stack,
// ignoring thresholds. It is the last resort when nothing else qualifies.
func (rt *Router) cheapestAvailable(stack *models.Stack) (*models.Profile, string) {
	best := map[string]int{tierLocal: 0, tierCheap: 1, tierPremium: 2, tierUnknown: 3}

	var chosen *models.Profile
	chosenRank := 99
	chosenTier := ""

	for _, id := range stack.Profiles {
		p, err := rt.Registry.Find(id)
		if err != nil || !rt.isAvailable(p) {
			continue
		}
		tier := tierOf(p)
		if rank := best[tier]; rank < chosenRank {
			chosen, chosenRank, chosenTier = p, rank, tier
		}
	}
	return chosen, chosenTier
}

// tierOf normalises a profile's cost tier.
func tierOf(p *models.Profile) string {
	t := strings.TrimSpace(strings.ToLower(p.CostTier))
	switch t {
	case tierLocal, tierCheap, tierPremium:
		return t
	case "":
		// Fall back to provider kind when the tier is unset.
		if p.IsLocal() {
			return tierLocal
		}
		return tierUnknown
	default:
		return tierUnknown
	}
}

// isAvailable reports whether a profile can actually serve a request now.
func (rt *Router) isAvailable(p *models.Profile) bool {
	if p.ProviderKind == models.ProviderGoogle {
		// Not implemented yet; avoid false availability.
		return false
	}
	if p.IsLocal() {
		// A local profile is only usable if the server is actually listening.
		if rt.Health == nil {
			return true
		}
		return rt.Health.Up(p.BaseURL)
	}
	if p.APIKeyCredential != "" {
		return rt.Credentials.Has(p.APIKeyCredential)
	}
	return false
}

func (rt *Router) Available(p *models.Profile) bool {
	return rt.isAvailable(p)
}
