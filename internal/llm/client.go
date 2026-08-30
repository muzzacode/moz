package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"
	"github.com/muzzacode/moz/internal/tools"

	openai "github.com/sashabaranov/go-openai"
)

type StreamEvent struct {
	Content string
	Done    bool
	Err     error
}

type ChatResponse struct {
	Content   string
	ToolCalls []ToolCall
	Usage     openai.Usage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// DefaultRequestTimeout bounds a single model request. Streaming replies reset
// no timer, so this is a hard ceiling on one request/response cycle.
const DefaultRequestTimeout = 5 * time.Minute

type Client struct {
	Profile     *models.Profile
	Credentials *credentials.Manager
	// Timeout bounds each request. Zero uses DefaultRequestTimeout.
	Timeout time.Duration
}

func New(p *models.Profile, cm *credentials.Manager) *Client {
	if cm == nil {
		cm = credentials.New()
	}
	return &Client{Profile: p, Credentials: cm}
}

// WithTimeout returns a copy of the client using the supplied request timeout.
func (c *Client) WithTimeout(d time.Duration) *Client {
	clone := *c
	clone.Timeout = d
	return &clone
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultRequestTimeout
}

// withDeadline derives a bounded context. The caller must always call cancel.
func (c *Client) withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout())
}

func (c *Client) isAnthropic() bool {
	return c.Profile.ProviderKind == models.ProviderAnthropic
}

func (c *Client) anthropicClient() (*AnthropicClient, error) {
	return newAnthropicClient(c.Profile, c.Credentials)
}

func (c *Client) apiClient() (*openai.Client, error) {
	if !c.Profile.CanUseOpenAIClient() {
		return nil, fmt.Errorf("provider %s is not supported yet", c.Profile.ProviderKind)
	}

	apiKey := ""
	if c.Profile.APIKeyCredential != "" {
		if v, err := c.Credentials.Get(c.Profile.APIKeyCredential); err == nil {
			apiKey = v
		}
	}

	cfg := openai.DefaultConfig(apiKey)
	if c.Profile.BaseURL != "" {
		cfg.BaseURL = c.Profile.BaseURL
	}
	return openai.NewClientWithConfig(cfg), nil
}

func (c *Client) buildMessages(messages []memory.Message) []openai.ChatCompletionMessage {
	var oaiMessages []openai.ChatCompletionMessage
	for _, m := range messages {
		msg := openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
				ID:   tc.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		oaiMessages = append(oaiMessages, msg)
	}
	return oaiMessages
}

func (c *Client) buildRequest(messages []memory.Message, toolDefs []tools.Definition) openai.ChatCompletionRequest {
	var oaiTools []openai.Tool
	for _, t := range toolDefs {
		oaiTools = append(oaiTools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	req := openai.ChatCompletionRequest{
		Model:     c.Profile.Model,
		Messages:  c.buildMessages(messages),
		Stream:    false,
		MaxTokens: 4096,
	}
	if len(oaiTools) > 0 {
		req.Tools = oaiTools
	}

	if temp, ok := c.Profile.DefaultParams["temperature"].(float64); ok {
		req.Temperature = float32(temp)
	}
	if max, ok := c.Profile.DefaultParams["max_tokens"].(int); ok {
		req.MaxTokens = max
	}

	return req
}

type rawCall struct {
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	Parameters json.RawMessage `json:"parameters"`
}

func (r rawCall) toToolCall(id string) ToolCall {
	args := r.Arguments
	if len(args) == 0 && len(r.Parameters) > 0 {
		args = r.Parameters
	}
	return ToolCall{ID: id, Name: r.Name, Arguments: args}
}

func parseContentToolCalls(content string) []ToolCall {
	if content == "" {
		return nil
	}

	// Try a code block.
	if idx := strings.Index(content, "```json"); idx >= 0 {
		block := content[idx+len("```json"):]
		if end := strings.Index(block, "```"); end >= 0 {
			content = strings.TrimSpace(block[:end])
		}
	} else if idx := strings.Index(content, "```"); idx >= 0 {
		block := content[idx+3:]
		if end := strings.Index(block, "```"); end >= 0 {
			content = strings.TrimSpace(block[:end])
		}
	}

	// Find the first JSON object or array in the content.
	start := -1
	for i, r := range content {
		if r == '{' || r == '[' {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	data := []byte(content[start:])

	now := time.Now().UnixNano()

	// Try an array of tool calls.
	if data[0] == '[' {
		var arr []rawCall
		if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 && arr[0].Name != "" {
			calls := make([]ToolCall, 0, len(arr))
			for i, r := range arr {
				calls = append(calls, r.toToolCall(fmt.Sprintf("call_%d_%d", now, i)))
			}
			return calls
		}
		return nil
	}

	// Try a wrapper object with a tool_calls field.
	var wrapper struct {
		ToolCalls []rawCall `json:"tool_calls"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.ToolCalls) > 0 {
		calls := make([]ToolCall, 0, len(wrapper.ToolCalls))
		for i, r := range wrapper.ToolCalls {
			calls = append(calls, r.toToolCall(fmt.Sprintf("call_%d_%d", now, i)))
		}
		return calls
	}

	// Try a single tool call object.
	var single rawCall
	if err := json.Unmarshal(data, &single); err == nil && single.Name != "" {
		return []ToolCall{single.toToolCall(fmt.Sprintf("call_%d", now))}
	}

	return nil
}

func (c *Client) Chat(ctx context.Context, messages []memory.Message, toolDefs []tools.Definition) (*ChatResponse, error) {
	if c.isAnthropic() {
		client, err := c.anthropicClient()
		if err != nil {
			return nil, err
		}
		reqCtx, cancel := c.withDeadline(ctx)
		defer cancel()
		resp, err := client.Chat(reqCtx, messages, toolDefs)
		if err != nil {
			return nil, annotateTimeout(ctx, reqCtx, c.timeout(), err)
		}
		return resp, nil
	}

	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}

	reqCtx, cancel := c.withDeadline(ctx)
	defer cancel()

	req := c.buildRequest(messages, toolDefs)
	resp, err := client.CreateChatCompletion(reqCtx, req)
	if err != nil {
		return nil, annotateTimeout(ctx, reqCtx, c.timeout(), fmt.Errorf("chat request failed: %w", err))
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := resp.Choices[0]
	cr := &ChatResponse{
		Content: choice.Message.Content,
		Usage:   resp.Usage,
	}

	for _, tc := range choice.Message.ToolCalls {
		if tc.Function.Name == "" {
			continue
		}
		cr.ToolCalls = append(cr.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	// Fallback for local models that emit tool calls as plain JSON in content.
	if len(cr.ToolCalls) == 0 && cr.Content != "" {
		if calls := parseContentToolCalls(cr.Content); len(calls) > 0 {
			cr.ToolCalls = calls
			cr.Content = ""
		}
	}

	return cr, nil
}

func (c *Client) ChatStream(ctx context.Context, messages []memory.Message, out chan<- StreamEvent) {
	defer close(out)

	if c.isAnthropic() {
		client, err := c.anthropicClient()
		if err != nil {
			out <- StreamEvent{Err: err}
			return
		}
		client.ChatStream(ctx, messages, out)
		return
	}

	client, err := c.apiClient()
	if err != nil {
		out <- StreamEvent{Err: err}
		return
	}

	reqCtx, cancel := c.withDeadline(ctx)
	defer cancel()

	req := c.buildRequest(messages, nil)
	req.Stream = true

	stream, err := client.CreateChatCompletionStream(reqCtx, req)
	if err != nil {
		out <- StreamEvent{Err: annotateTimeout(ctx, reqCtx, c.timeout(), fmt.Errorf("failed to start stream: %w", err))}
		return
	}
	defer stream.Close()

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			out <- StreamEvent{Done: true}
			return
		}
		if err != nil {
			out <- StreamEvent{Err: annotateTimeout(ctx, reqCtx, c.timeout(), fmt.Errorf("stream error: %w", err))}
			return
		}
		if len(resp.Choices) > 0 {
			out <- StreamEvent{Content: resp.Choices[0].Delta.Content}
		}
	}
}

// annotateTimeout distinguishes a user-initiated cancellation from a provider
// that simply stopped responding. Without this the user sees an opaque
// "context canceled" and cannot tell which happened.
func annotateTimeout(parent, req context.Context, limit time.Duration, err error) error {
	if parent.Err() != nil {
		return context.Canceled
	}
	if req.Err() != nil {
		return fmt.Errorf("model did not respond within %s: %w", limit, err)
	}
	return err
}
