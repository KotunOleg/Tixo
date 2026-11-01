//go:build live

package agent

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveClaudeAsksForTheSearchTool(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("set ANTHROPIC_API_KEY to send one real request to Claude")
	}
	model := os.Getenv("AGENT_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sink := &collect{}
	reply, err := NewClaude(key, model).Next(ctx, Request{
		System:   systemPrompt("en", time.Now()),
		Lang:     "en",
		Messages: []Message{textMessage(RoleUser, "Any rock concerts in Kyiv next week?")},
		Tools: []Tool{{
			Name:        "search_events",
			Description: "Find upcoming published events by text, city, genre and dates.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string"}, "city": map[string]any{"type": "string"}, "genre": map[string]any{"type": "string"},
			}},
		}},
	}, sink)
	if err != nil {
		t.Fatal(err)
	}
	var called bool
	for _, b := range reply.Blocks {
		called = called || (b.Type == BlockToolUse && b.Name == "search_events")
	}
	if reply.StopReason != StopToolUse || !called {
		t.Fatalf("stop %q, blocks %+v, want a search_events call", reply.StopReason, reply.Blocks)
	}
	t.Logf("thinking summary: %q", sink.thinking.String())
}
