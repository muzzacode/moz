package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/tools"
	openai "github.com/sashabaranov/go-openai"
)

type AnthropicClient struct {
	client  anthropic.Client
	profile *models.Profile
}

func newAnthropicClient(p *models.Profile, cm *credentials.Manager) (*AnthropicClient, error) {
	apiKey := ""
	if p.APIKeyCredential != "" {
		if v, err := cm.Get(p.APIKeyCredential); err == nil {
			apiKey = v
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("missing %s", p.APIKeyCredential)
	}

	opts := []option.RequestOption{option.WithAPIKey(apiKey)}

	workspaceID := os.Getenv("ANTHROPIC_WORKSPACE_ID")
	if workspaceID == "" && cm != nil {
		if v, err := cm.Get("ANTHROPIC_WORKSPACE_ID"); err == nil {
			workspaceID = v
		}
	}
	if workspaceID != "" {
		opts = append(opts, option.WithHeader("anthropic-workspace-id", workspaceID))
	}

	client := anthropic.NewClient(opts...)
	return &AnthropicClient{client: client, profile: p}, nil
}

func (c *AnthropicClient) Chat(ctx context.Context, messages []memory.Message, toolDefs []tools.Definition) (*ChatResponse, error) {
	system, anthropicMessages := c.convertMessages(messages)
	tools := c.convertTools(toolDefs)

	req := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.profile.Model),
		MaxTokens: 4096,
		System:    system,
		Messages:  anthropicMessages,
	}

	if tools != nil {
		req.Tools = tools
	}

	if temp, ok := c.profile.DefaultParams["temperature"].(float64); ok {
		req.Temperature = anthropic.Float(temp)
	}
	if max, ok := c.profile.DefaultParams["max_tokens"].(int); ok {
		req.MaxTokens = int64(max)
	}

	resp, err := c.client.Messages.New(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat failed: %w", err)
	}

	return c.parseResponse(resp)
}

func (c *AnthropicClient) ChatStream(ctx context.Context, messages []memory.Message, out chan<- StreamEvent) {
	system, anthropicMessages := c.convertMessages(messages)

	req := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.profile.Model),
		MaxTokens: 4096,
		System:    system,
		Messages:  anthropicMessages,
	}

	if temp, ok := c.profile.DefaultParams["temperature"].(float64); ok {
		req.Temperature = anthropic.Float(temp)
	}
	if max, ok := c.profile.DefaultParams["max_tokens"].(int); ok {
		req.MaxTokens = int64(max)
	}

	stream := c.client.Messages.NewStreaming(ctx, req)
	for stream.Next() {
		event := stream.Current()
		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch delta := ev.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				out <- StreamEvent{Content: delta.Text}
			}
		}
	}

	if err := stream.Err(); err != nil {
		out <- StreamEvent{Err: fmt.Errorf("anthropic stream error: %w", err)}
		return
	}
	out <- StreamEvent{Done: true}
}

func (c *AnthropicClient) convertMessages(messages []memory.Message) (system []anthropic.TextBlockParam, out []anthropic.MessageParam) {
	var systemText []string
	for _, m := range messages {
		if m.Role == "system" {
			systemText = append(systemText, m.Content)
			continue
		}

		role := m.Role
		if role == "tool" {
			role = "user"
		}

		var blocks []anthropic.ContentBlockParamUnion

		if m.Role == "tool" {
			isError := strings.HasPrefix(m.Content, "error:")
			content := m.Content
			if isError {
				content = strings.TrimPrefix(content, "error: ")
			}
			blocks = append(blocks, anthropic.NewToolResultBlock(m.ToolCallID, content, isError))
		} else {
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				var input any
				_ = json.Unmarshal([]byte(tc.Arguments), &input)
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
		}

		if role == "user" {
			out = append(out, anthropic.NewUserMessage(blocks...))
		} else {
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		}
	}

	for _, s := range systemText {
		system = append(system, anthropic.TextBlockParam{Text: s})
	}
	return
}

func (c *AnthropicClient) convertTools(toolDefs []tools.Definition) []anthropic.ToolUnionParam {
	if len(toolDefs) == 0 {
		return nil
	}

	var out []anthropic.ToolUnionParam
	for _, t := range toolDefs {
		schema := anthropic.ToolInputSchemaParam{}
		if props, ok := t.Parameters["properties"]; ok {
			schema.Properties = props
		}
		if required, ok := t.Parameters["required"].([]string); ok {
			schema.Required = required
		}

		desc := t.Description
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(desc),
			InputSchema: schema,
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out
}

func openaiUsage(u anthropic.Usage) openai.Usage {
	return openai.Usage{
		PromptTokens:     int(u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens),
		CompletionTokens: int(u.OutputTokens),
		TotalTokens:      int(u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens),
	}
}

func (c *AnthropicClient) parseResponse(resp *anthropic.Message) (*ChatResponse, error) {
	cr := &ChatResponse{
		Usage: openaiUsage(resp.Usage),
	}

	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			cr.Content += b.Text
		case anthropic.ToolUseBlock:
			cr.ToolCalls = append(cr.ToolCalls, ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: json.RawMessage(b.Input),
			})
		}
	}

	return cr, nil
}
