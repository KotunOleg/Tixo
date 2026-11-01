package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KotunOleg/Tixo/internal/events"
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
	turn := fakeState(req.Messages)
	question, last := turn.question, turn.last
	buying := wantsToBuy(question)

	if last == nil {
		if err := sink.Thinking(lines.thinking); err != nil {
			return Reply{}, err
		}
		args := map[string]any{"query": question, "limit": fakeSearchLimit}
		if buying {
			args = map[string]any{"limit": events.SearchMaxLimit}
		}
		return fakeCall(Block{Type: BlockThinking, Text: lines.thinking}, "s1", tools.NameSearchEvents, args), nil
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
			return fakeOpen(sink, lines, turn, found.Events)
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
			return fakeOpen(sink, lines, turn, turn.found)
		}
		tier := ev.Tiers[0]
		for _, t := range ev.Tiers {
			if t.Available > 0 {
				tier = t
				break
			}
		}
		return fakeCall(Block{}, "b1", tools.NameBookTicket, map[string]any{
			"event_slug": ev.Slug, "tier_id": tier.TierID, "quantity": 1, "idempotency_key": "fake-" + ev.Slug,
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

type fakeTurn struct {
	question string
	last     *fakeTool
	found    []tools.EventSummary
	opened   map[string]bool
}

func fakeState(msgs []Message) fakeTurn {
	turn := fakeTurn{opened: map[string]bool{}}
	start := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].isUserText() {
			turn.question, start = msgs[i].Blocks[0].Text, i
			break
		}
	}
	calls := map[string]string{}
	for _, m := range msgs[start:] {
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockToolUse:
				calls[b.ID] = b.Name
				if b.Name == tools.NameGetEvent {
					var in tools.GetEventInput
					if json.Unmarshal(b.Input, &in) == nil {
						turn.opened[in.Slug] = true
					}
				}
			case BlockToolResult:
				turn.last = &fakeTool{ID: b.ToolUseID, Name: calls[b.ToolUseID], Result: b}
				var found tools.SearchEventsOutput
				if calls[b.ToolUseID] == tools.NameSearchEvents && json.Unmarshal([]byte(b.Text), &found) == nil {
					turn.found = found.Events
				}
			}
		}
	}
	return turn
}

func fakeOpen(sink Sink, lines fakeLines, turn fakeTurn, found []tools.EventSummary) (Reply, error) {
	for _, e := range found {
		if e.PriceFromUAH == nil || turn.opened[e.Slug] {
			continue
		}
		if err := sink.Thinking(lines.searching); err != nil {
			return Reply{}, err
		}
		id := fmt.Sprintf("g%d", len(turn.opened)+1)
		return fakeCall(Block{Type: BlockThinking, Text: lines.searching}, id, tools.NameGetEvent, map[string]any{"slug": e.Slug}), nil
	}
	return fakeAnswer(sink, lines.refuse)
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
