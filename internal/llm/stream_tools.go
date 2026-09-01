package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/muzzacode/moz/internal/memory"
	"github.com/muzzacode/moz/internal/tools"

	openai "github.com/sashabaranov/go-openai"
)

// StreamResult is the outcome of a single streamed request.
type StreamResult struct {
	Content   string
	ToolCalls []ToolCall
	Usage     openai.Usage
}

// OnDelta receives content as it arrives.
type OnDelta func(string)

// ChatStreamTools performs one streamed request that can also return tool calls.
//
// The agent previously made a blocking request to decide whether tools were
// needed, discarded the answer, and then made a second request purely to stream
// the same text. That doubled both cost and latency on every reply, and the long
// pause before anything appeared was the first request completing.
//
// Streaming with tools attached collapses that into one call: text is emitted as
// it arrives, and tool-call fragments are reassembled from the same stream.
func (c *Client) ChatStreamTools(
	ctx context.Context,
	messages []memory.Message,
	toolDefs []tools.Definition,
	onDelta OnDelta,
) (*StreamResult, error) {
	// Anthropic's streaming path does not carry tool calls here, so it uses a
	// single non-streaming request. Still one call, not two.
	if c.isAnthropic() {
		resp, err := c.Chat(ctx, messages, toolDefs)
		if err != nil {
			return nil, err
		}
		if len(resp.ToolCalls) == 0 && resp.Content != "" && onDelta != nil {
			onDelta(resp.Content)
		}
		return &StreamResult{Content: resp.Content, ToolCalls: resp.ToolCalls, Usage: resp.Usage}, nil
	}

	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}

	req := c.buildRequest(messages, toolDefs)
	req.Stream = true
	// Usage is normally omitted from streamed responses, but the budget needs it.
	req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}

	reqCtx, cancel := c.withDeadline(ctx)
	defer cancel()

	stream, err := withRetry(ctx, c.retries(), c.Notify, func() (*openai.ChatCompletionStream, error) {
		s, err := client.CreateChatCompletionStream(reqCtx, req)
		if err != nil {
			return nil, annotateTimeout(ctx, reqCtx, c.timeout(), fmt.Errorf("failed to start stream: %w", err))
		}
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var content strings.Builder
	acc := newToolCallAccumulator()
	result := &StreamResult{}

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, annotateTimeout(ctx, reqCtx, c.timeout(), fmt.Errorf("stream error: %w", err))
		}

		// The usage-only final chunk carries no choices.
		if resp.Usage != nil {
			result.Usage = *resp.Usage
		}
		if len(resp.Choices) == 0 {
			continue
		}

		delta := resp.Choices[0].Delta
		if delta.Content != "" {
			content.WriteString(delta.Content)
			if onDelta != nil {
				onDelta(delta.Content)
			}
		}
		acc.add(delta.ToolCalls)
	}

	result.Content = content.String()
	result.ToolCalls = acc.finish()

	// Models without native tool calling emit JSON in the body instead.
	if len(result.ToolCalls) == 0 && result.Content != "" && !c.Profile.SupportsNativeTools() {
		if calls := parseContentToolCalls(result.Content); len(calls) > 0 {
			result.ToolCalls = calls
			result.Content = ""
		}
	}

	return result, nil
}

// toolCallAccumulator reassembles tool calls from streamed fragments.
//
// A streamed tool call arrives in pieces: the name usually in the first chunk
// and the arguments spread across later ones, keyed by index rather than id.
type toolCallAccumulator struct {
	byIndex map[int]*partialCall
}

type partialCall struct {
	id   string
	name string
	args strings.Builder
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{byIndex: make(map[int]*partialCall)}
}

func (a *toolCallAccumulator) add(deltas []openai.ToolCall) {
	for _, d := range deltas {
		idx := 0
		if d.Index != nil {
			idx = *d.Index
		}
		p, ok := a.byIndex[idx]
		if !ok {
			p = &partialCall{}
			a.byIndex[idx] = p
		}
		if d.ID != "" {
			p.id = d.ID
		}
		if d.Function.Name != "" {
			p.name = d.Function.Name
		}
		p.args.WriteString(d.Function.Arguments)
	}
}

// finish returns the completed calls in index order.
func (a *toolCallAccumulator) finish() []ToolCall {
	if len(a.byIndex) == 0 {
		return nil
	}

	idxs := make([]int, 0, len(a.byIndex))
	for i := range a.byIndex {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)

	out := make([]ToolCall, 0, len(idxs))
	for _, i := range idxs {
		p := a.byIndex[i]
		if p.name == "" {
			continue
		}
		args := strings.TrimSpace(p.args.String())
		// An empty argument list must still be valid JSON for the executor.
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			// A truncated stream can leave arguments unparseable; surfacing the
			// tool with empty arguments produces a clear error downstream rather
			// than a silent misfire.
			args = "{}"
		}
		out = append(out, ToolCall{ID: p.id, Name: p.name, Arguments: json.RawMessage(args)})
	}
	return out
}
