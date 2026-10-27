package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

func mustOrder(t *testing.T, q *dbgen.Queries, userID int64, tier dbgen.TicketTier, key pgtype.UUID) (dbgen.Order, error) {
	t.Helper()
	return q.CreateOrder(context.Background(), dbgen.CreateOrderParams{
		UserID: userID, EventID: tier.EventID, TierID: tier.ID, Quantity: 2,
		UnitPrice: tier.Price, Total: 2 * tier.Price, IdempotencyKey: key,
	})
}

func TestIdempotencyKeyIsUniquePerUserNotGlobally(t *testing.T) {
	_, q := newQueries(t)
	a := mustOrganizer(t, q, "org-a")
	ev := mustEvent(t, q, a.ID, mustVenue(t, q, a.ID, "A hall").ID, "show")
	tier := mustTier(t, q, a.ID, ev.ID, "Standard", 10)
	alice := mustUser(t, q, "alice@example.com")
	bob := mustUser(t, q, "bob@example.com")
	key := pgtype.UUID{Bytes: [16]byte{1, 2, 3}, Valid: true}

	if _, err := mustOrder(t, q, alice.ID, tier, key); err != nil {
		t.Fatal(err)
	}
	_, err := mustOrder(t, q, alice.ID, tier, key)
	wantPgCode(t, "same user reuses key", err, uniqueViolation)
	if _, err := mustOrder(t, q, bob.ID, tier, key); err != nil {
		t.Errorf("another user with the same key must get their own order, got %v", err)
	}
}

func TestOrderedTierAndEventCannotBeDeleted(t *testing.T) {
	pool, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	ev := mustEvent(t, q, a.ID, mustVenue(t, q, a.ID, "A hall").ID, "show")
	tier := mustTier(t, q, a.ID, ev.ID, "Standard", 10)
	alice := mustUser(t, q, "alice@example.com")
	if _, err := mustOrder(t, q, alice.ID, tier, pgtype.UUID{Bytes: [16]byte{9}, Valid: true}); err != nil {
		t.Fatal(err)
	}

	_, err := pool.Exec(ctx, `DELETE FROM ticket_tiers WHERE id = $1`, tier.ID)
	wantPgCode(t, "delete tier with an order", err, foreignKeyViolation)
	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, alice.ID)
	wantPgCode(t, "delete user with an order", err, foreignKeyViolation)
}

func TestCreateTicketsIssuesOneUniqueCodePerSeat(t *testing.T) {
	_, q := newQueries(t)
	ctx := context.Background()
	a := mustOrganizer(t, q, "org-a")
	ev := mustEvent(t, q, a.ID, mustVenue(t, q, a.ID, "A hall").ID, "show")
	tier := mustTier(t, q, a.ID, ev.ID, "Standard", 10)
	alice := mustUser(t, q, "alice@example.com")
	order, err := mustOrder(t, q, alice.ID, tier, pgtype.UUID{Bytes: [16]byte{7}, Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	tickets, err := q.CreateTickets(ctx, dbgen.CreateTicketsParams{OrderID: order.ID, Quantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[pgtype.UUID]bool{}
	for _, tk := range tickets {
		if tk.OrderID != order.ID || tk.PdfKey.Valid {
			t.Errorf("ticket %+v: want order %d and no pdf yet", tk, order.ID)
		}
		codes[tk.Code] = true
	}
	if len(tickets) != 10 || len(codes) != 10 {
		t.Errorf("issued %d tickets with %d distinct codes, want 10 and 10", len(tickets), len(codes))
	}
}
