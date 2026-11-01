package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/KotunOleg/Tixo/internal/mcpserver"
	"github.com/KotunOleg/Tixo/internal/tools"
)

const (
	EventThinking        = "thinking"
	EventText            = "text"
	EventToolCall        = "tool_call"
	EventToolResult      = "tool_result"
	EventConfirmRequired = "confirm_required"
	EventError           = "error"
	EventDone            = "done"

	ErrUpstream     = "upstream"
	ErrRefusal      = "refusal"
	ErrTooManySteps = "too_many_steps"
	ErrInternal     = "internal"

	MaxSteps       = 8
	MaxMessageLen  = 2000
	clientName     = "tixo-assistant"
	pendingConfirm = "Not booked yet. The user now sees a confirmation card with this event, tier, quantity and total, and only their own click on Buy can purchase it. Tell them to check the card; never say the tickets are bought."
)

var ErrBadMessage = errors.New("message must be 1-2000 characters")

type Emit func(event string, data any) error

type Deps struct {
	Model   Model
	Tools   *tools.Tools
	MCP     *mcpserver.Server
	History *History
	Log     *slog.Logger
	Now     func() time.Time
}

type Agent struct {
	Deps
}

func New(d Deps) *Agent { return &Agent{Deps: d} }

type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResult struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

type Done struct {
	StopReason string `json:"stop_reason"`
	Truncated  bool   `json:"truncated"`
}

type Failure struct {
	Code string `json:"code"`
}

func CheckMessage(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > MaxMessageLen {
		return "", ErrBadMessage
	}
	return text, nil
}

func (a *Agent) Reset(ctx context.Context, userID int64) error {
	return a.History.Reset(ctx, userID)
}

func (a *Agent) Run(ctx context.Context, userID int64, lang, text string, emit Emit) error {
	text, err := CheckMessage(text)
	if err != nil {
		return err
	}
	history, err := a.History.Load(ctx, userID)
	if err != nil {
		return a.fail(emit, ErrInternal, err)
	}

	session, err := a.connect(ctx, userID)
	if err != nil {
		return a.fail(emit, ErrInternal, err)
	}
	defer func() { _ = session.Close() }()
	toolList, err := listTools(ctx, session)
	if err != nil {
		return a.fail(emit, ErrInternal, err)
	}

	sink := emitSink{emit: emit}
	turn := []Message{textMessage(RoleUser, text)}
	req := Request{System: systemPrompt(lang, a.Now()), Lang: lang, Tools: toolList}
	for step := 0; ; step++ {
		req.Messages = append(append([]Message{}, history...), turn...)
		reply, err := a.Model.Next(ctx, req, sink)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return a.fail(emit, ErrUpstream, err)
		}
		turn = append(turn, Message{Role: RoleAssistant, Blocks: reply.Blocks})

		switch reply.StopReason {
		case StopRefusal:
			return a.fail(emit, ErrRefusal, nil)
		case StopToolUse:
		default:
			if err := a.History.Append(ctx, userID, withoutThinking(withoutDanglingToolUse(turn))); err != nil {
				return a.fail(emit, ErrInternal, err)
			}
			return emit(EventDone, Done{StopReason: reply.StopReason, Truncated: reply.StopReason == StopMaxTokens})
		}

		if step+1 >= MaxSteps {
			return a.fail(emit, ErrTooManySteps, nil)
		}
		results, err := a.runTools(ctx, session, reply.Blocks, emit)
		if err != nil {
			return err
		}
		if len(results) == 0 {
			return a.fail(emit, ErrUpstream, errors.New("tool_use stop without tool_use blocks"))
		}
		turn = append(turn, Message{Role: RoleUser, Blocks: results})
	}
}

func (a *Agent) fail(emit Emit, code string, cause error) error {
	if cause != nil {
		a.Log.Error("assistant turn failed", "code", code, "error", cause)
	}
	if err := emit(EventError, Failure{Code: code}); err != nil {
		return err
	}
	return cause
}

func (a *Agent) connect(ctx context.Context, userID int64) (*mcp.ClientSession, error) {
	clientT, serverT := mcp.NewInMemoryTransports()
	if _, err := a.MCP.For(userID).Connect(ctx, serverT, nil); err != nil {
		return nil, fmt.Errorf("start mcp server: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "1"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		return nil, fmt.Errorf("connect mcp client: %w", err)
	}
	return session, nil
}

func listTools(ctx context.Context, session *mcp.ClientSession) ([]Tool, error) {
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list mcp tools: %w", err)
	}
	out := make([]Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("encode %s schema: %w", t.Name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			return nil, fmt.Errorf("decode %s schema: %w", t.Name, err)
		}
		out = append(out, Tool{Name: t.Name, Description: t.Description, Schema: schema})
	}
	return out, nil
}

func (a *Agent) runTools(ctx context.Context, session *mcp.ClientSession, blocks []Block, emit Emit) ([]Block, error) {
	var results []Block
	for _, b := range blocks {
		if b.Type != BlockToolUse {
			continue
		}
		if err := emit(EventToolCall, ToolCall{ID: b.ID, Name: b.Name, Input: b.Input}); err != nil {
			return nil, err
		}
		content, res, confirm := a.callTool(ctx, session, b)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err := emit(EventToolResult, res); err != nil {
			return nil, err
		}
		if confirm != nil {
			if err := emit(EventConfirmRequired, confirm); err != nil {
				return nil, err
			}
		}
		results = append(results, Block{Type: BlockToolResult, ToolUseID: b.ID, Text: content, IsError: !res.OK})
	}
	return results, nil
}

func (a *Agent) callTool(ctx context.Context, session *mcp.ClientSession, b Block) (string, ToolResult, *tools.Booking) {
	res := ToolResult{ID: b.ID, Name: b.Name}
	if b.Name == tools.NameBookTicket {
		var in tools.BookTicketInput
		if err := json.Unmarshal(b.Input, &in); err != nil {
			res.Error = "invalid book_ticket input"
			return res.Error, res, nil
		}
		booking, err := a.Tools.CheckBooking(ctx, in)
		if err != nil {
			res.Error = toolMessage(err)
			return res.Error, res, nil
		}
		res.OK, res.Data = true, booking
		return pendingConfirm, res, &booking
	}

	var args map[string]any
	if len(b.Input) > 0 {
		if err := json.Unmarshal(b.Input, &args); err != nil {
			res.Error = "tool input must be a JSON object"
			return res.Error, res, nil
		}
	}
	out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: b.Name, Arguments: args})
	if err != nil {
		a.Log.Warn("assistant tool call failed", "tool", b.Name, "error", err)
		res.Error = "tool " + b.Name + " is not available"
		return res.Error, res, nil
	}
	text := contentText(out)
	if out.IsError {
		res.Error = text
		return text, res, nil
	}
	res.OK, res.Data = true, out.StructuredContent
	return text, res, nil
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func toolMessage(err error) string {
	var terr *tools.Error
	if errors.As(err, &terr) {
		return terr.Error()
	}
	return "internal error, try again later"
}

type emitSink struct{ emit Emit }

func (s emitSink) Thinking(delta string) error {
	return s.emit(EventThinking, map[string]string{"text": delta})
}

func (s emitSink) Text(delta string) error {
	return s.emit(EventText, map[string]string{"text": delta})
}
