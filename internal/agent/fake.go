package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KotunOleg/Tixo/internal/tools"
)

const fakeSearchLimit = 3

var buyWords = []string{"купи", "купити", "придбай", "забронюй", "buy", "book"}

type Fake struct{}

type fakeLines struct {
	thinking, searching, retry, found, none, confirm, refuse string
}

var fakeText = map[string]fakeLines{
	"uk": {
		thinking:  "Демо-режим без ШІ: шукаю події через інструменти Tixo.",
		searching: "Перевіряю подію, щоб підготувати картку підтвердження.",
		retry:     "За точним текстом нічого немає, дивлюсь найближчі події.",
		found:     "Ось що знайшов:",
		none:      "Нічого не знайшов. Спробуйте інше місто, жанр або дату.",
		confirm:   "Перевірте картку нижче: квиток купується лише після натискання «Купити».",
		refuse:    "Цю подію зараз не можна купити.",
	},
	"en": {
		thinking:  "Demo mode without AI: searching events with the Tixo tools.",
		searching: "Checking the event to prepare a confirmation card.",
		retry:     "Nothing matches that exact text, looking at upcoming events instead.",
		found:     "Here is what I found:",
		none:      "Nothing found. Try another city, genre or date.",
		confirm:   "Check the card below: the ticket is bought only when you press Buy.",
		refuse:    "This event cannot be bought right now.",
	},
}

func (Fake) Next(_ context.Context, req Request, sink Sink) (Reply, error) {
	lines, ok := fakeText[req.Lang]
	if !ok {
		lines = fakeText["uk"]
	}
	question, last := fakeState(req.Messages)
	buying := wantsToBuy(question)

	if last == nil {
		if err := sink.Thinking(lines.thinking); err != nil {
			return Reply{}, err
		}
		query := question
		if buying {
			query = ""
		}
		return fakeCall(Block{Type: BlockThinking, Text: lines.thinking}, "s1", tools.NameSearchEvents, map[string]any{"query": query, "limit": fakeSearchLimit}), nil
	}

	switch last.Name {
	case tools.NameSearchEvents:
		var found tools.SearchEventsOutput
		_ = json.Unmarshal([]byte(last.Result.Text), &found)
		switch {
		case last.Result.IsError || len(found.Events) == 0 && last.ID == "s1":
			if err := sink.Thinking(lines.retry); err != nil {
				return Reply{}, err
			}
			return fakeCall(Block{Type: BlockThinking, Text: lines.retry}, "s2", tools.NameSearchEvents, map[string]any{"limit": fakeSearchLimit}), nil
		case len(found.Events) == 0:
			return fakeAnswer(sink, lines.none)
		case buying:
			if err := sink.Thinking(lines.searching); err != nil {
				return Reply{}, err
			}
			return fakeCall(Block{Type: BlockThinking, Text: lines.searching}, "g1", tools.NameGetEvent, map[string]any{"slug": found.Events[0].Slug}), nil
		}
		var b strings.Builder
		b.WriteString(lines.found)
		for _, e := range found.Events {
			fmt.Fprintf(&b, "\n%s — %s, %s", e.Title, e.Venue, strings.Replace(e.StartsAt[:16], "T", " ", 1))
		}
		return fakeAnswer(sink, b.String())
	case tools.NameGetEvent:
		var ev tools.Event
		if err := json.Unmarshal([]byte(last.Result.Text), &ev); err != nil || last.Result.IsError || len(ev.Tiers) == 0 || !ev.OnSale {
			return fakeAnswer(sink, lines.refuse)
		}
		return fakeCall(Block{}, "b1", tools.NameBookTicket, map[string]any{
			"event_slug": ev.Slug, "tier_id": ev.Tiers[0].TierID, "quantity": 1, "idempotency_key": "fake-" + ev.Slug,
		}), nil
	default:
		if last.Result.IsError {
			return fakeAnswer(sink, lines.refuse)
		}
		return fakeAnswer(sink, lines.confirm)
	}
}

type fakeTool struct {
	ID, Name string
	Result   Block
}

func fakeState(msgs []Message) (string, *fakeTool) {
	var question string
	start := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].isUserText() {
			question, start = msgs[i].Blocks[0].Text, i
			break
		}
	}
	var last *fakeTool
	calls := map[string]string{}
	for _, m := range msgs[start:] {
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockToolUse:
				calls[b.ID] = b.Name
			case BlockToolResult:
				last = &fakeTool{ID: b.ToolUseID, Name: calls[b.ToolUseID], Result: b}
			}
		}
	}
	return question, last
}

func wantsToBuy(text string) bool {
	text = strings.ToLower(text)
	for _, w := range buyWords {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func fakeCall(thinking Block, id, name string, input map[string]any) Reply {
	raw, _ := json.Marshal(input)
	var blocks []Block
	if thinking.Type != "" {
		blocks = append(blocks, thinking)
	}
	blocks = append(blocks, Block{Type: BlockToolUse, ID: id, Name: name, Input: raw})
	return Reply{Blocks: blocks, StopReason: StopToolUse}
}

func fakeAnswer(sink Sink, text string) (Reply, error) {
	for _, chunk := range strings.SplitAfter(text, " ") {
		if err := sink.Text(chunk); err != nil {
			return Reply{}, err
		}
	}
	return Reply{Blocks: []Block{{Type: BlockText, Text: text}}, StopReason: StopEndTurn}, nil
}
