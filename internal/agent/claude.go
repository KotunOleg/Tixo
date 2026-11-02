package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	MaxTokens  = 2048
	maxRetries = 2
)

type Claude struct {
	client anthropic.Client
	model  string
}

func NewClaude(apiKey, model string, opts ...option.RequestOption) *Claude {
	opts = append([]option.RequestOption{option.WithAPIKey(apiKey), option.WithMaxRetries(maxRetries)}, opts...)
	return &Claude{client: anthropic.NewClient(opts...), model: model}
}

func (c *Claude) Next(ctx context.Context, req Request, sink Sink) (Reply, error) {
	params, err := c.params(req)
	if err != nil {
		return Reply{}, err
	}
	stream := c.client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	msg := anthropic.Message{}
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return Reply{}, fmt.Errorf("accumulate claude stream: %w", err)
		}
		delta, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent)
		if !ok {
			continue
		}
		switch d := delta.Delta.AsAny().(type) {
		case anthropic.ThinkingDelta:
			err = sink.Thinking(d.Thinking)
		case anthropic.TextDelta:
			err = sink.Text(d.Text)
		}
		if err != nil {
			return Reply{}, err
		}
	}
	if err := stream.Err(); err != nil {
		return Reply{}, fmt.Errorf("claude stream: %w", err)
	}
	return Reply{Blocks: fromClaude(msg.Content), StopReason: string(msg.StopReason)}, nil
}

func (c *Claude) params(req Request) (anthropic.MessageNewParams, error) {
	msgs := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		blocks, err := toClaude(m.Blocks)
		if err != nil {
			return anthropic.MessageNewParams{}, err
		}
		if m.Role == RoleAssistant {
			msgs = append(msgs, anthropic.NewAssistantMessage(blocks...))
		} else {
			msgs = append(msgs, anthropic.NewUserMessage(blocks...))
		}
	}
	toolParams := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
	for _, t := range req.Tools {
		schema := anthropic.ToolInputSchemaParam{Properties: t.Schema["properties"]}
		if req, ok := t.Schema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					schema.Required = append(schema.Required, s)
				}
			}
		}
		toolParams = append(toolParams, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name: t.Name, Description: anthropic.String(t.Description), InputSchema: schema,
		}})
	}
	thinking := anthropic.ThinkingConfigAdaptiveParam{Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized}
	return anthropic.MessageNewParams{
		Model:        c.model,
		MaxTokens:    MaxTokens,
		System:       []anthropic.TextBlockParam{{Text: req.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Messages:     msgs,
		Tools:        toolParams,
		Thinking:     anthropic.ThinkingConfigParamUnion{OfAdaptive: &thinking},
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortMedium},
	}, nil
}

func toClaude(blocks []Block) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			out = append(out, anthropic.NewTextBlock(b.Text))
		case BlockThinking:
			out = append(out, anthropic.NewThinkingBlock(b.Signature, b.Text))
		case BlockRedactedThinking:
			out = append(out, anthropic.NewRedactedThinkingBlock(b.Text))
		case BlockToolUse:
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out = append(out, anthropic.NewToolUseBlock(b.ID, input, b.Name))
		case BlockToolResult:
			out = append(out, anthropic.NewToolResultBlock(b.ToolUseID, b.Text, b.IsError))
		default:
			return nil, fmt.Errorf("unknown block type %q", b.Type)
		}
	}
	return out, nil
}

func fromClaude(content []anthropic.ContentBlockUnion) []Block {
	out := make([]Block, 0, len(content))
	for _, c := range content {
		switch v := c.AsAny().(type) {
		case anthropic.TextBlock:
			out = append(out, Block{Type: BlockText, Text: v.Text})
		case anthropic.ThinkingBlock:
			out = append(out, Block{Type: BlockThinking, Text: v.Thinking, Signature: v.Signature})
		case anthropic.RedactedThinkingBlock:
			out = append(out, Block{Type: BlockRedactedThinking, Text: v.Data})
		case anthropic.ToolUseBlock:
			out = append(out, Block{Type: BlockToolUse, ID: v.ID, Name: v.Name, Input: v.Input})
		}
	}
	return out
}
