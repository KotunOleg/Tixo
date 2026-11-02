package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

type collect struct{ thinking, text strings.Builder }

func (c *collect) Thinking(d string) error { c.thinking.WriteString(d); return nil }
func (c *collect) Text(d string) error     { c.text.WriteString(d); return nil }

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(e), &probe)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, e)
	}
	return b.String()
}

func TestClaudeStreamsDeltasAndReturnsBlocksForTheNextRequest(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Need a search."}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Searching"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu1","name":"search_events","input":{}}}`,
			`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"rock\"}"}}`,
			`{"type":"content_block_stop","index":2}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
			`{"type":"message_stop"}`,
		))
	}))
	defer srv.Close()

	c := NewClaude("test-key", "claude-sonnet-5-5", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	sink := &collect{}
	reply, err := c.Next(context.Background(), Request{
		System: "system prompt",
		Messages: []Message{
			textMessage(RoleUser, "rock?"),
			{Role: RoleAssistant, Blocks: []Block{{Type: BlockThinking, Text: "t", Signature: "s"}, {Type: BlockToolUse, ID: "a", Name: "get_event", Input: json.RawMessage(`{"slug":"x"}`)}}},
			{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolUseID: "a", Text: "not_found", IsError: true}}},
		},
		Tools: []Tool{{Name: "search_events", Description: "Find events", Schema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []any{"query"}}}},
	}, sink)
	if err != nil {
		t.Fatal(err)
	}

	if sink.thinking.String() != "Need a search." || sink.text.String() != "Searching" {
		t.Errorf("streamed thinking %q and text %q, want the deltas as they came", sink.thinking.String(), sink.text.String())
	}
	if reply.StopReason != StopToolUse || len(reply.Blocks) != 3 {
		t.Fatalf("reply = %+v, want thinking, text and tool_use with stop tool_use", reply)
	}
	if th := reply.Blocks[0]; th.Type != BlockThinking || th.Signature != "sig-1" {
		t.Errorf("thinking block = %+v, want its signature kept so the tool loop can replay it", th)
	}
	if tu := reply.Blocks[2]; tu.ID != "tu1" || string(tu.Input) != `{"query":"rock"}` {
		t.Errorf("tool_use = %+v, want the accumulated input", tu)
	}

	thinking, _ := body["thinking"].(map[string]any)
	if thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Errorf("thinking = %v, want adaptive with summarized display so steps are visible", body["thinking"])
	}
	if body["max_tokens"] != float64(MaxTokens) || body["stream"] != true || body["model"] != "claude-sonnet-5-5" {
		t.Errorf("request = model %v, max_tokens %v, stream %v", body["model"], body["max_tokens"], body["stream"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v, want 3", body["messages"])
	}
	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["is_error"] != true || result["tool_use_id"] != "a" {
		t.Errorf("tool_result sent as %v", result)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "search_events" || fmt.Sprint(tool["input_schema"].(map[string]any)["required"]) != "[query]" {
		t.Errorf("tool sent as %v", tool)
	}
}
