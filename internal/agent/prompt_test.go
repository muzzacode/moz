package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/project"
)

// A native-tools provider must not be taught a JSON output contract, because
// combining it with real tool schemas produces malformed calls.
func TestNativePromptOmitsJSONContract(t *testing.T) {
	p := buildSystemPrompt(true)
	for _, banned := range []string{`{"name":`, `{"tool_calls":`, "ONLY a JSON object"} {
		if strings.Contains(p, banned) {
			t.Fatalf("native prompt must not contain %q", banned)
		}
	}
	if !strings.Contains(p, "tool-calling interface") {
		t.Fatal("native prompt should point at the tool-calling interface")
	}
}

func TestTextPromptIncludesJSONContractAndToolNames(t *testing.T) {
	p := buildSystemPrompt(false)
	if !strings.Contains(p, "ONLY a JSON object") {
		t.Fatal("text prompt must define the JSON contract")
	}
	for _, tool := range []string{"read_file", "edit_file", "write_file", "mark_done", "web_fetch"} {
		if !strings.Contains(p, tool) {
			t.Fatalf("text prompt must list %q since there is no schema", tool)
		}
	}
}

// Behavioural rules must not depend on the tool-call transport.
func TestBothPromptsShareBehaviouralRules(t *testing.T) {
	native := buildSystemPrompt(true)
	text := buildSystemPrompt(false)
	for _, rule := range []string{
		"never use it on an existing file",
		"Never edit files through exec",
		"add_todo",
	} {
		if !strings.Contains(native, rule) {
			t.Fatalf("native prompt missing rule %q", rule)
		}
		if !strings.Contains(text, rule) {
			t.Fatalf("text prompt missing rule %q", rule)
		}
	}
}

func TestAppendProjectContext(t *testing.T) {
	if got := appendProjectContext("base", "", nil); got != "base" {
		t.Fatalf("nothing to add should leave the prompt unchanged, got %q", got)
	}
	got := appendProjectContext("base", "make ci", nil)
	if !strings.Contains(got, "make ci") {
		t.Fatalf("verification command missing: %q", got)
	}
}

// A project's own instructions must be present and must be told to take
// precedence over Moz's generic defaults.
func TestAppendProjectContextIncludesInstructions(t *testing.T) {
	ins := &project.Instructions{Source: "AGENTS.md", Content: "Requires Java 25."}
	got := appendProjectContext("base", "mvn test", ins)

	for _, want := range []string{"base", "mvn test", "AGENTS.md", "Requires Java 25.", "override"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// Instructions come last so they carry the most weight.
	if strings.Index(got, "AGENTS.md") < strings.Index(got, "mvn test") {
		t.Fatal("instructions should follow the verification command")
	}
}

func TestSupportsNativeToolsByProvider(t *testing.T) {
	cases := []struct {
		kind models.ProviderKind
		caps []models.Capability
		want bool
	}{
		{models.ProviderOpenRouter, []models.Capability{models.CapToolCalling}, true},
		{models.ProviderAnthropic, []models.Capability{models.CapToolCalling}, true},
		{models.ProviderOpenAICompatible, []models.Capability{models.CapToolCalling}, true},
		// Local models vary in tool support, so they keep the text fallback.
		{models.ProviderOllama, []models.Capability{models.CapToolCalling}, false},
		// Capability is required regardless of provider.
		{models.ProviderOpenRouter, nil, false},
	}
	for _, tc := range cases {
		p := &models.Profile{ProviderKind: tc.kind, Capabilities: tc.caps}
		if got := p.SupportsNativeTools(); got != tc.want {
			t.Fatalf("%s caps=%v: got %v want %v", tc.kind, tc.caps, got, tc.want)
		}
	}
}

// A model that fails and gets swapped must report why, or a broken key is
// indistinguishable from a broken request.
func TestShortErrExtractsProviderMessage(t *testing.T) {
	err := fmt.Errorf(`chat request failed: error, status code: 402, status: 402 Payment Required, body: {"error":{"message":"This request requires more credits, or fewer max_tokens.","code":402}}`)
	got := shortErr(err)
	if !strings.Contains(got, "more credits") {
		t.Fatalf("expected the provider message to survive, got %q", got)
	}
	if strings.Contains(got, `"code"`) {
		t.Fatalf("expected the JSON envelope to be stripped, got %q", got)
	}
}

func TestShortErrCollapsesAndTruncates(t *testing.T) {
	got := shortErr(fmt.Errorf("a\n\nb   c"))
	if got != "a b c" {
		t.Fatalf("expected whitespace collapsed, got %q", got)
	}
	long := shortErr(fmt.Errorf("%s", strings.Repeat("x", 500)))
	if len(long) > 200 {
		t.Fatalf("expected truncation, got %d chars", len(long))
	}
}
