package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type pollStep struct {
	updates []Update
	err     error
}

type scriptedClient struct {
	fakeClient
	steps   []pollStep
	offsets []int64
	cancel  context.CancelFunc
}

func (s *scriptedClient) GetUpdates(_ context.Context, offset int64, _ time.Duration) ([]Update, error) {
	s.offsets = append(s.offsets, offset)
	if len(s.steps) == 0 {
		s.cancel()
		return nil, context.Canceled
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step.updates, step.err
}

type recordingHandler struct {
	ids []int64
}

func (h *recordingHandler) HandleUpdate(_ context.Context, u Update) error {
	h.ids = append(h.ids, u.UpdateID)
	if u.UpdateID == 11 {
		return errors.New("database is down")
	}
	return nil
}

func runPoll(t *testing.T, steps ...pollStep) (*scriptedClient, *recordingHandler, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &scriptedClient{steps: steps, cancel: cancel}
	h := &recordingHandler{}
	err := Poll(ctx, client, h, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return client, h, err
}

func TestPollConfirmsEachUpdateSoNoneIsHandledTwice(t *testing.T) {
	client, h, err := runPoll(t,
		pollStep{updates: []Update{{UpdateID: 10}, {UpdateID: 11}}},
		pollStep{},
		pollStep{updates: []Update{{UpdateID: 12}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.ids; len(got) != 3 || got[0] != 10 || got[1] != 11 || got[2] != 12 {
		t.Errorf("handled %v, want 10 11 12 once each even though 11 failed", got)
	}
	want := []int64{0, 12, 12, 13}
	if len(client.offsets) != len(want) {
		t.Fatalf("offsets = %v, want %v", client.offsets, want)
	}
	for i := range want {
		if client.offsets[i] != want[i] {
			t.Errorf("offsets = %v, want %v", client.offsets, want)
			break
		}
	}
}

func TestPollStopsWithAClearErrorWhileAWebhookIsSet(t *testing.T) {
	_, _, err := runPoll(t, pollStep{err: ErrConflict})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "telegram-set-webhook --delete") {
		t.Errorf("err = %v, want a conflict error that says how to remove the webhook", err)
	}
}

func TestPollKeepsGoingAfterATransientError(t *testing.T) {
	_, h, err := runPoll(t, pollStep{err: errors.New("telegram: getUpdates: status 502")}, pollStep{updates: []Update{{UpdateID: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.ids) != 1 || h.ids[0] != 5 {
		t.Errorf("handled %v after a 502, want update 5", h.ids)
	}
}
