package llm

import (
	"context"
	"fmt"
	"io"

	"github.com/muzzacode/moz/internal/credentials"
	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/models"

	openai "github.com/sashabaranov/go-openai"
)

type StreamEvent struct {
	Content string
	Done    bool
	Err     error
}

type Client struct {
	Profile     *models.Profile
	Credentials *credentials.Manager
}

func New(p *models.Profile, cm *credentials.Manager) *Client {
	if cm == nil {
		cm = credentials.New()
	}
	return &Client{Profile: p, Credentials: cm}
}

func (c *Client) ChatStream(ctx context.Context, messages []memory.Message, out chan<- StreamEvent) {
	defer close(out)

	if !c.Profile.CanUseOpenAIClient() {
		out <- StreamEvent{Err: fmt.Errorf("provider %s is not supported yet", c.Profile.ProviderKind)}
		return
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
	client := openai.NewClientWithConfig(cfg)

	var oaiMessages []openai.ChatCompletionMessage
	for _, m := range messages {
		oaiMessages = append(oaiMessages, openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	req := openai.ChatCompletionRequest{
		Model:     c.Profile.Model,
		Messages:  oaiMessages,
		Stream:    true,
		MaxTokens: 4096,
	}

	if temp, ok := c.Profile.DefaultParams["temperature"].(float64); ok {
		req.Temperature = float32(temp)
	}
	if max, ok := c.Profile.DefaultParams["max_tokens"].(int); ok {
		req.MaxTokens = max
	}

	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		out <- StreamEvent{Err: fmt.Errorf("failed to start stream: %w", err)}
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
			out <- StreamEvent{Err: fmt.Errorf("stream error: %w", err)}
			return
		}
		if len(resp.Choices) > 0 {
			out <- StreamEvent{Content: resp.Choices[0].Delta.Content}
		}
	}
}
