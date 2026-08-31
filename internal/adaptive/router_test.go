package adaptive

import (
	"context"
	"strings"
	"testing"

	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/models"
	openai "github.com/sashabaranov/go-openai"
)

const localURL = "http://127.0.0.1:11434/v1/"

// testRegistry mirrors the real shape: a local model, a cheap cloud model, and a
// frontier model, ordered cheapest first in the stack.
func testRegistry() *models.Registry {
	return &models.Registry{
		Profiles: []models.Profile{
			{
				ID: "local-coder", Name: "Local Coder",
				ProviderKind: models.ProviderOllama, BaseURL: localURL,
				CostTier: "local", ContextLength: 131072,
				Capabilities: []models.Capability{models.CapToolCalling, models.CapCode},
			},
			{
				ID: "cheap-cloud", Name: "Cheap Cloud",
				ProviderKind: models.ProviderOpenRouter, APIKeyCredential: "TEST_CHEAP_KEY",
				CostTier: "cloud-cheap", ContextLength: 200000,
				Capabilities: []models.Capability{models.CapToolCalling, models.CapCode},
			},
			{
				ID: "frontier", Name: "Frontier",
				ProviderKind: models.ProviderAnthropic, APIKeyCredential: "TEST_FRONTIER_KEY",
				CostTier: "cloud-premium", ContextLength: 200000,
				Capabilities: []models.Capability{models.CapToolCalling, models.CapReasoning},
			},
		},
		Stacks: []models.Stack{
			{Name: "code", Class: models.TaskCodeEdit, Profiles: []string{"local-coder", "cheap-cloud", "frontier"}},
			{Name: "reasoning", Class: models.TaskReasoning, Profiles: []string{"local-coder", "cheap-cloud", "frontier"}},
			{Name: "architecture", Class: models.TaskArchitecture, Profiles: []string{"local-coder", "cheap-cloud", "frontier"}},
			{Name: "chat", Class: models.TaskChat, Profiles: []string{"local-coder", "cheap-cloud"}},
			{Name: "daily", Class: models.TaskQuickChat, Profiles: []string{"local-coder", "cheap-cloud"}},
		},
	}
}

// testRouter wires deterministic health and credentials so routing decisions are
// reproducible without a live Ollama or real keys.
func testRouter(t *testing.T, localUp bool, keys ...string) *Router {
	t.Helper()

	creds := credentials.New()
	for _, k := range keys {
		creds.Set(k, "test-value")
	}

	rt := New(testRegistry(), creds)
	rt.Health = NewHealthChecker()
	rt.Health.Probe = func(context.Context, string) bool { return localUp }
	return rt
}

// A local profile must not be considered available when Ollama is not running.
// Previously this returned true unconditionally, so adaptive mode routed to a
// dead server and the turn failed.
func TestLocalUnavailableWhenServerDown(t *testing.T) {
	rt := testRouter(t, false, "TEST_CHEAP_KEY")

	d, err := rt.Select("refactor the invoice service and fix the failing test")
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID == "local-coder" {
		t.Fatal("routed to local model while the server is down")
	}
	if d.Profile.ID != "cheap-cloud" {
		t.Fatalf("expected fallback to cheap cloud, got %s", d.Profile.ID)
	}
}

func TestLocalUsedWhenServerUp(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY")

	d, err := rt.Select("hello")
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID != "local-coder" {
		t.Fatalf("expected the local model for a trivial prompt, got %s", d.Profile.ID)
	}
	if d.Tier != tierLocal {
		t.Fatalf("expected local tier, got %q", d.Tier)
	}
}

// With no local server and no keys there is genuinely nothing to run.
func TestNoModelAvailableIsExplained(t *testing.T) {
	rt := testRouter(t, false)

	_, err := rt.Select("do something")
	if err == nil {
		t.Fatal("expected an error when nothing is available")
	}
	if !strings.Contains(err.Error(), "Ollama") {
		t.Fatalf("error should hint at the cause, got %q", err)
	}
}

// A trivial prompt must not reach paid inference just because a key exists.
func TestTrivialTaskStaysLocal(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")

	d, _ := rt.Select("hi")
	if d.Profile.ID != "local-coder" {
		t.Fatalf("trivial prompt should stay local, got %s", d.Profile.ID)
	}
}

// The frontier tier requires clearing a higher bar than plain cloud.
func TestFrontierRequiresHighConfidence(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")
	rt.PreferLocal = false

	// "explain" scores 0.7: above the cloud threshold, below the frontier one.
	mid, _ := rt.Select("explain the trade-offs of this approach")
	if mid.Profile.ID != "cheap-cloud" {
		t.Fatalf("mid-confidence task should use cheap cloud, got %s (%s)", mid.Profile.ID, mid.Reason)
	}

	// Architecture keywords score 0.9, clearing the frontier threshold.
	hard, _ := rt.Select("design the system architecture for multi-region scalability")
	if hard.Profile.ID != "frontier" {
		t.Fatalf("high-confidence task should reach frontier, got %s (%s)", hard.Profile.ID, hard.Reason)
	}
}

// CloudThreshold was previously declared in config and never read.
func TestCloudThresholdIsHonoured(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY")
	// Isolate threshold behaviour from the prefer-local bias.
	rt.PreferLocal = false

	// An impossible bar keeps everything local.
	rt.CloudThreshold = 0.99
	d, _ := rt.Select("design the system architecture for scalability")
	if d.Profile.ID != "local-coder" {
		t.Fatalf("a high threshold should keep work local, got %s", d.Profile.ID)
	}

	// A trivial bar lets even simple work leave local.
	rt.CloudThreshold = 0.01
	rt.PremiumThreshold = 0.99
	d2, _ := rt.Select("refactor this function")
	if d2.Profile.ID != "cheap-cloud" {
		t.Fatalf("a low threshold should promote to cloud, got %s", d2.Profile.ID)
	}
}

// A spent budget must force cheaper routing rather than continuing to bill.
func TestExhaustedBudgetForcesLocal(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")
	rt.PreferLocal = false
	rt.Budget = NewBudget(1.00)
	rt.Budget.AddCost(1.50) // over the ceiling

	d, err := rt.Select("design the system architecture for multi-region scalability")
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID != "local-coder" {
		t.Fatalf("expected a downgrade to local, got %s", d.Profile.ID)
	}
	if !d.Downgraded {
		t.Fatal("decision should be flagged as downgraded")
	}
	if !strings.Contains(d.Reason, "budget") {
		t.Fatalf("reason should explain the budget, got %q", d.Reason)
	}
}

// With the budget spent and no local server, the task should still run on the
// cheapest option rather than failing outright.
func TestExhaustedBudgetStillRunsWhenNoLocal(t *testing.T) {
	rt := testRouter(t, false, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")
	rt.Budget = NewBudget(0.50)
	rt.Budget.AddCost(5.00)

	d, err := rt.Select("design the system architecture for scalability")
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID != "cheap-cloud" {
		t.Fatalf("expected the cheapest available model, got %s", d.Profile.ID)
	}
	if !d.Downgraded {
		t.Fatal("expected a downgraded decision")
	}
}

func TestUnavailableCloudIsSkipped(t *testing.T) {
	// Frontier key missing, so a hard task must settle for cheap cloud.
	rt := testRouter(t, false, "TEST_CHEAP_KEY")

	d, _ := rt.Select("design the system architecture for scalability")
	if d.Profile.ID != "cheap-cloud" {
		t.Fatalf("expected cheap cloud, got %s", d.Profile.ID)
	}
}

// An unrecognised CostTier must not be preferred over a known-cheaper model.
// Treating it as free would let a mislabelled paid profile become the default.
func TestUnknownTierNotPreferredOverLocal(t *testing.T) {
	reg := testRegistry()
	reg.Profiles = append(reg.Profiles, models.Profile{
		ID: "mystery", Name: "Mystery",
		ProviderKind: models.ProviderOpenRouter, APIKeyCredential: "TEST_CHEAP_KEY",
		CostTier: "who-knows",
	})
	// Deliberately list the unknown-tier model first.
	reg.Stacks = []models.Stack{
		{Name: "daily", Class: models.TaskQuickChat, Profiles: []string{"mystery", "local-coder"}},
		{Name: "chat", Class: models.TaskChat, Profiles: []string{"mystery", "local-coder"}},
	}

	creds := credentials.New()
	creds.Set("TEST_CHEAP_KEY", "v")
	rt := New(reg, creds)
	rt.Health = NewHealthChecker()
	rt.Health.Probe = func(context.Context, string) bool { return true }

	d, err := rt.Select("hi")
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID != "local-coder" {
		t.Fatalf("a trivial task should use the local model, not an unknown paid tier: got %s", d.Profile.ID)
	}
}

// When an unknown-tier model is the only option it must still be usable, since
// refusing to run at all would be worse.
func TestUnknownTierUsedWhenOnlyOption(t *testing.T) {
	reg := testRegistry()
	reg.Profiles = append(reg.Profiles, models.Profile{
		ID: "mystery", Name: "Mystery",
		ProviderKind: models.ProviderOpenRouter, APIKeyCredential: "TEST_CHEAP_KEY",
		CostTier: "who-knows",
	})
	reg.Stacks = []models.Stack{
		{Name: "daily", Class: models.TaskQuickChat, Profiles: []string{"mystery"}},
		{Name: "chat", Class: models.TaskChat, Profiles: []string{"mystery"}},
	}

	creds := credentials.New()
	creds.Set("TEST_CHEAP_KEY", "v")
	rt := New(reg, creds)
	rt.Health = NewHealthChecker()
	rt.Health.Probe = func(context.Context, string) bool { return false }

	d, err := rt.Select("hi")
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.ID != "mystery" {
		t.Fatalf("expected the only available model, got %s", d.Profile.ID)
	}
}

func TestEscalateMovesToNextTierUp(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")

	local, _ := rt.Registry.Find("local-coder")
	next := rt.Escalate(local, models.TaskCodeEdit)
	if next == nil {
		t.Fatal("expected an escalation target")
	}
	// Cheapest upgrade, not the most capable.
	if next.ID != "cheap-cloud" {
		t.Fatalf("expected cheap-cloud, got %s", next.ID)
	}
}

func TestEscalateFromCheapReachesFrontier(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")

	cheap, _ := rt.Registry.Find("cheap-cloud")
	next := rt.Escalate(cheap, models.TaskCodeEdit)
	if next == nil || next.ID != "frontier" {
		t.Fatalf("expected frontier, got %+v", next)
	}
}

// Retrying the most expensive model is unlikely to help and doubles the cost.
func TestEscalateStopsAtFrontier(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")

	frontier, _ := rt.Registry.Find("frontier")
	if next := rt.Escalate(frontier, models.TaskCodeEdit); next != nil {
		t.Fatalf("frontier should not escalate, got %s", next.ID)
	}
}

func TestEscalateRefusesWhenBudgetSpent(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")
	rt.Budget = NewBudget(1.0)
	rt.Budget.AddCost(2.0)

	local, _ := rt.Registry.Find("local-coder")
	if next := rt.Escalate(local, models.TaskCodeEdit); next != nil {
		t.Fatalf("a spent budget must block escalation, got %s", next.ID)
	}
}

func TestEscalateReturnsNilWhenNothingBetter(t *testing.T) {
	// No cloud keys at all.
	rt := testRouter(t, true)

	local, _ := rt.Registry.Find("local-coder")
	if next := rt.Escalate(local, models.TaskCodeEdit); next != nil {
		t.Fatalf("expected no target, got %s", next.ID)
	}
}

// A provider failure must not drop a hard task to the cheapest model. Degrading
// one tier keeps capability close to what the task needed.
func TestFallbackDegradesOneTierNotToCheapest(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")

	frontier, _ := rt.Registry.Find("frontier")
	next := rt.Fallback(frontier, models.TaskCodeEdit)
	if next == nil {
		t.Fatal("expected a fallback target")
	}
	if next.ID != "cheap-cloud" {
		t.Fatalf("expected a one-tier downgrade to cheap-cloud, got %s", next.ID)
	}
}

func TestFallbackReachesLocalWhenNothingElseWorks(t *testing.T) {
	// No cheap-cloud key, so the only option below frontier is local.
	rt := testRouter(t, true, "TEST_FRONTIER_KEY")

	frontier, _ := rt.Registry.Find("frontier")
	next := rt.Fallback(frontier, models.TaskCodeEdit)
	if next == nil || next.ID != "local-coder" {
		t.Fatalf("expected local fallback, got %+v", next)
	}
}

// Local is the cheapest tier, so there is nothing to fall back to.
func TestFallbackFromLocalReturnsNil(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY")

	local, _ := rt.Registry.Find("local-coder")
	if next := rt.Fallback(local, models.TaskCodeEdit); next != nil {
		t.Fatalf("local has no cheaper tier, got %s", next.ID)
	}
}

// With the ceiling reached, only free inference is an acceptable fallback.
func TestFallbackRespectsBudgetCeiling(t *testing.T) {
	rt := testRouter(t, true, "TEST_CHEAP_KEY", "TEST_FRONTIER_KEY")
	rt.Budget = NewBudget(1.0)
	rt.Budget.AddCost(2.0)

	frontier, _ := rt.Registry.Find("frontier")
	next := rt.Fallback(frontier, models.TaskCodeEdit)
	if next == nil {
		t.Fatal("expected a fallback to free inference")
	}
	if next.ID != "local-coder" {
		t.Fatalf("a spent budget should force local, got %s", next.ID)
	}
}

func TestBudgetAccounting(t *testing.T) {
	b := NewBudget(1.0)
	if b.Exhausted() {
		t.Fatal("a fresh budget must not be exhausted")
	}
	b.AddCost(0.4)
	if b.Exhausted() {
		t.Fatal("still under the ceiling")
	}
	if got := b.Remaining(); got < 0.59 || got > 0.61 {
		t.Fatalf("unexpected remaining %.4f", got)
	}
	b.AddCost(0.7)
	if !b.Exhausted() {
		t.Fatal("should be exhausted past the ceiling")
	}
	if b.Remaining() != 0 {
		t.Fatalf("remaining should clamp to 0, got %.4f", b.Remaining())
	}

	b.Reset()
	if b.Exhausted() {
		t.Fatal("reset should clear spend")
	}
}

func TestBudgetUnlimitedByDefault(t *testing.T) {
	b := NewBudget(0)
	b.AddCost(1000)
	if b.Exhausted() {
		t.Fatal("a zero limit means unlimited")
	}
	if b.Remaining() != -1 {
		t.Fatalf("unlimited should report -1, got %.2f", b.Remaining())
	}
}

// Budget must charge using the profile's real prices.
func TestBudgetAddUsesProfilePricing(t *testing.T) {
	b := NewBudget(0)
	b.Add("claude-sonnet-5", openai.Usage{PromptTokens: 1_000_000, CompletionTokens: 0})
	if got := b.Spent(); got < 1.9 || got > 2.1 {
		t.Fatalf("expected about $2.00 for 1M input tokens, got %.4f", got)
	}

	// A local model is free and must not accumulate spend.
	b2 := NewBudget(0)
	b2.Add("coding-default", openai.Usage{PromptTokens: 5_000_000, CompletionTokens: 5_000_000})
	if b2.Spent() != 0 {
		t.Fatalf("local inference should cost nothing, got %.4f", b2.Spent())
	}
}

func TestHealthCheckerCachesAndRechecks(t *testing.T) {
	h := NewHealthChecker()
	calls := 0
	h.Probe = func(context.Context, string) bool {
		calls++
		return true
	}

	for i := 0; i < 5; i++ {
		if !h.Up(localURL) {
			t.Fatal("expected up")
		}
	}
	if calls != 1 {
		t.Fatalf("expected the result to be cached, got %d probes", calls)
	}

	h.Invalidate(localURL)
	if !h.Up(localURL) {
		t.Fatal("expected up after invalidation")
	}
	if calls != 2 {
		t.Fatalf("expected a re-probe after invalidation, got %d", calls)
	}
}

func TestHealthCheckerEmptyURLIsDown(t *testing.T) {
	h := NewHealthChecker()
	if h.Up("") {
		t.Fatal("an empty base URL cannot be up")
	}
}

func TestNewWithOptionsAppliesConfig(t *testing.T) {
	rt := NewWithOptions(testRegistry(), credentials.New(), Options{
		PreferLocal:      false,
		CloudThreshold:   0.25,
		PremiumThreshold: 0.9,
		MaxSessionCost:   5,
	}, nil)

	if rt.PreferLocal {
		t.Fatal("PreferLocal not applied")
	}
	if rt.CloudThreshold != 0.25 || rt.PremiumThreshold != 0.9 {
		t.Fatal("thresholds not applied")
	}
	if rt.Budget == nil || rt.Budget.Limit() != 5 {
		t.Fatal("budget not applied")
	}
}
