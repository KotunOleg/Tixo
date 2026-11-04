package agent

import (
	"context"
	"encoding/json"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	BlockText             = "text"
	BlockThinking         = "thinking"
	BlockRedactedThinking = "redacted_thinking"
	BlockToolUse          = "tool_use"
	BlockToolResult       = "tool_result"

	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
)

type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type Message struct {
	Role   string  `json:"role"`
	Blocks []Block `json:"blocks"`
}

type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
}

type Request struct {
	System   string
	Lang     string
	Messages []Message
	Tools    []Tool
}

type Reply struct {
	Blocks     []Block
	StopReason string
}

type Sink interface {
	Thinking(delta string) error
	Text(delta string) error
}

type Model interface {
	Next(ctx context.Context, req Request, sink Sink) (Reply, error)
}

func textMessage(role, text string) Message {
	return Message{Role: role, Blocks: []Block{{Type: BlockText, Text: text}}}
}

func (m Message) isUserText() bool {
	return m.Role == RoleUser && len(m.Blocks) > 0 && m.Blocks[0].Type == BlockText
}

func withoutThinking(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			if b.Type == BlockThinking || b.Type == BlockRedactedThinking {
				continue
			}
			blocks = append(blocks, b)
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, Message{Role: m.Role, Blocks: blocks})
	}
	return out
}

func withoutDanglingToolUse(msgs []Message) []Message {
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != RoleAssistant {
		return msgs
	}
	last := msgs[len(msgs)-1]
	blocks := make([]Block, 0, len(last.Blocks))
	for _, b := range last.Blocks {
		if b.Type != BlockToolUse {
			blocks = append(blocks, b)
		}
	}
	out := append([]Message{}, msgs[:len(msgs)-1]...)
	return append(out, Message{Role: RoleAssistant, Blocks: blocks})
}
