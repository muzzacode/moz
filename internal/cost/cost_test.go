package cost

import (
	"testing"

	"github.com/muzzacode/moz/internal/models"
	openai "github.com/sashabaranov/go-openai"
)

// A profile with no price entry is silently free, which would let the session
// budget be enforced against zero while real money is spent.
func TestEveryProfileHasAPrice(t *testing.T) {
	for _, p := range models.DefaultProfiles().Profiles {
		if _, ok := knownPrices[p.ID]; !ok {
			t.Fatalf("profile %q has no price entry, so its spend would not be counted", p.ID)
		}
	}
}

// Prices must not contradict the tier a profile advertises, or routing would
// treat an expensive model as cheap.
func TestPricesAgreeWithCostTiers(t *testing.T) {
	for _, p := range models.DefaultProfiles().Profiles {
		price := knownPrices[p.ID]
		switch p.CostTier {
		case "local":
			if price.Input != 0 || price.Output != 0 {
				t.Fatalf("local profile %q has a non-zero price", p.ID)
			}
		case "cloud-cheap":
			// A "cheap" model costing dollars per million tokens is mislabelled.
			if price.Input > 0.60 {
				t.Fatalf("profile %q is tiered cloud-cheap but costs $%.2f/M input", p.ID, price.Input)
			}
		case "cloud-premium":
			if price.Input > 0 && price.Input < 0.60 {
				t.Fatalf("profile %q is tiered cloud-premium but costs only $%.2f/M input", p.ID, price.Input)
			}
		default:
			t.Fatalf("profile %q has an unrecognised cost tier %q", p.ID, p.CostTier)
		}
	}
}

func TestEstimateChargesInputAndOutputSeparately(t *testing.T) {
	// GLM Flash is $0.075 in and $0.250 out per million.
	got := Estimate("glm-flash", openai.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000})
	want := 0.075 + 0.250
	if got < want-0.001 || got > want+0.001 {
		t.Fatalf("expected about $%.3f, got $%.4f", want, got)
	}
}

func TestEstimateUnknownProfileIsFree(t *testing.T) {
	if got := Estimate("not-a-profile", openai.Usage{PromptTokens: 1_000_000}); got != 0 {
		t.Fatalf("unknown profiles should not be charged, got %.4f", got)
	}
}

func TestFormatHidesZeroAndShowsSmallSpend(t *testing.T) {
	if got := Format("local-coder", openai.Usage{PromptTokens: 100_000}); got != "" {
		t.Fatalf("free inference should render nothing, got %q", got)
	}
	if got := Format("glm-flash", openai.Usage{PromptTokens: 1000}); got != "<$0.01" {
		t.Fatalf("tiny spend should render as <$0.01, got %q", got)
	}
	if got := Format("claude-opus-5", openai.Usage{PromptTokens: 1_000_000}); got == "" || got == "<$0.01" {
		t.Fatalf("real spend should render a figure, got %q", got)
	}
}

// The workhorse must genuinely be far cheaper than the frontier, or the tiering
// achieves nothing.
func TestWorkhorseIsOrdersOfMagnitudeCheaperThanFrontier(t *testing.T) {
	cheap := knownPrices["glm-flash"]
	frontier := knownPrices["claude-opus-5"]

	if ratio := frontier.Input / cheap.Input; ratio < 10 {
		t.Fatalf("expected the frontier tier to cost at least 10x more, got %.1fx", ratio)
	}
}
