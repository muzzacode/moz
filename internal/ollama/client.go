package ollama

import (
	"context"
	"fmt"
	"io"

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
	Profile *models.Profile
}

func New(p *models.Profile) *Client {
	return &Client{Profile: p}
}

func (c *Client) ChatStream(ctx context.Context, messages []memory.Message, out chan<- StreamEvent) {
	defer close(out)
	cfg := openai.DefaultConfig("")
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
