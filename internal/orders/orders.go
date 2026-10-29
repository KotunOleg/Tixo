package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	StatusPaid = "paid"

	TopicTicketsIssue = "tickets.issue"

	FieldTier     = "tier_id"
	FieldQuantity = "quantity"
	FieldKey      = "idempotency_key"

	CodeRequired = "required"
	CodeRange    = "range"
	CodeInvalid  = "invalid"

	MinQuantity = 1
	MaxQuantity = 10

	uniqueViolation = "23505"
	keyConstraint   = "orders_user_idempotency_key"
	eventPublished  = "published"
	eventDraft      = "draft"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrNotOnSale = errors.New("event is not on sale")
	ErrSoldOut   = errors.New("not enough tickets left")
	ErrKeyReused = errors.New("idempotency key was used for another purchase")
)

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for f, code := range e.Fields {
		parts = append(parts, f+": "+code)
	}
	slices.Sort(parts)
	return "invalid order: " + strings.Join(parts, ", ")
}

type outboxPayload struct {
	OrderID int64 `json:"order_id"`
}

type Input struct {
	EventID  int64
	TierID   int64
	Quantity int32
	Key      uuid.UUID
}

type Details struct {
	dbgen.Order
	EventSlug     string
	EventTitle    string
	EventStartsAt time.Time
	EventStatus   string
	TierName      string
	VenueName     string
	VenueCity     string
	Tickets       []dbgen.Ticket
}

type Service struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: dbgen.New(pool)}
}

func (s *Service) Purchase(ctx context.Context, userID int64, in Input) (Details, bool, error) {
	if err := validate(in); err != nil {
		return Details{}, false, err
	}
	existing, found, err := s.byKey(ctx, userID, in.Key)
	if err != nil {
		return Details{}, false, err
	}
	if found {
		return s.replay(ctx, userID, existing, in)
	}

	order, err := s.reserve(ctx, userID, in)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == keyConstraint {
		winner, _, err := s.byKey(ctx, userID, in.Key)
		if err != nil {
			return Details{}, false, err
		}
		return s.replay(ctx, userID, winner, in)
	}
	if err != nil {
		return Details{}, false, err
	}
	d, err := s.Get(ctx, userID, order.ID)
	return d, false, err
}

func (s *Service) reserve(ctx context.Context, userID int64, in Input) (dbgen.Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbgen.Order{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	tier, err := q.ReserveSeats(ctx, dbgen.ReserveSeatsParams{
		Quantity: in.Quantity, TierID: in.TierID, EventID: pgtype.Int8{Int64: in.EventID, Valid: in.EventID != 0},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Order{}, whyNotReserved(ctx, q, in)
	}
	if err != nil {
		return dbgen.Order{}, fmt.Errorf("reserve seats: %w", err)
	}
	if tier.Price > math.MaxInt64/int64(in.Quantity) {
		return dbgen.Order{}, fmt.Errorf("order total overflows for tier %d", tier.ID)
	}
	order, err := q.CreateOrder(ctx, dbgen.CreateOrderParams{
		UserID: userID, EventID: tier.EventID, TierID: tier.ID, Quantity: in.Quantity,
		UnitPrice: tier.Price, Total: tier.Price * int64(in.Quantity), IdempotencyKey: pgKey(in.Key),
	})
	if err != nil {
		return dbgen.Order{}, fmt.Errorf("create order: %w", err)
	}
	if _, err := q.CreateTickets(ctx, dbgen.CreateTicketsParams{OrderID: order.ID, Quantity: in.Quantity}); err != nil {
		return dbgen.Order{}, fmt.Errorf("create tickets: %w", err)
	}
	payload, err := json.Marshal(outboxPayload{OrderID: order.ID})
	if err != nil {
		return dbgen.Order{}, fmt.Errorf("marshal outbox payload: %w", err)
	}
	if _, err := q.InsertOutbox(ctx, dbgen.InsertOutboxParams{Topic: TopicTicketsIssue, Payload: payload}); err != nil {
		return dbgen.Order{}, fmt.Errorf("insert outbox: %w", err)
	}
	return order, tx.Commit(ctx)
}

func whyNotReserved(ctx context.Context, q *dbgen.Queries, in Input) error {
	st, err := q.GetTierSaleState(ctx, in.TierID)
	switch {
	case errors.Is(err, pgx.ErrNoRows), err == nil && st.Status == eventDraft:
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("tier sale state: %w", err)
	case in.EventID != 0 && st.EventID != in.EventID:
		return &ValidationError{Fields: map[string]string{FieldTier: CodeInvalid}}
	case st.Status != eventPublished || !st.Upcoming:
		return ErrNotOnSale
	default:
		return ErrSoldOut
	}
}

func (s *Service) byKey(ctx context.Context, userID int64, key uuid.UUID) (dbgen.Order, bool, error) {
	o, err := s.q.GetOrderByKey(ctx, dbgen.GetOrderByKeyParams{UserID: userID, IdempotencyKey: pgKey(key)})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Order{}, false, nil
	}
	if err != nil {
		return dbgen.Order{}, false, fmt.Errorf("order by key: %w", err)
	}
	return o, true, nil
}

func (s *Service) replay(ctx context.Context, userID int64, existing dbgen.Order, in Input) (Details, bool, error) {
	if existing.TierID != in.TierID || existing.Quantity != in.Quantity || (in.EventID != 0 && existing.EventID != in.EventID) {
		return Details{}, false, ErrKeyReused
	}
	d, err := s.Get(ctx, userID, existing.ID)
	return d, true, err
}

func (s *Service) Get(ctx context.Context, userID, id int64) (Details, error) {
	row, err := s.q.GetOrderForUser(ctx, dbgen.GetOrderForUserParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Details{}, ErrNotFound
	}
	if err != nil {
		return Details{}, fmt.Errorf("get order: %w", err)
	}
	tickets, err := s.q.ListTicketsForOrder(ctx, id)
	if err != nil {
		return Details{}, fmt.Errorf("list tickets: %w", err)
	}
	return Details{
		Order: row.Order, EventSlug: row.EventSlug, EventTitle: row.EventTitle, EventStartsAt: row.EventStartsAt,
		EventStatus: row.EventStatus, TierName: row.TierName, VenueName: row.VenueName, VenueCity: row.VenueCity,
		Tickets: tickets,
	}, nil
}

func (s *Service) List(ctx context.Context, userID int64, limit, offset int32) ([]Details, int32, error) {
	total, err := s.q.CountOrdersForUser(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	rows, err := s.q.ListOrdersForUser(ctx, dbgen.ListOrdersForUserParams{UserID: userID, Lim: limit, Off: offset})
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	out := make([]Details, len(rows))
	for i, r := range rows {
		out[i] = Details{
			Order: r.Order, EventSlug: r.EventSlug, EventTitle: r.EventTitle, EventStartsAt: r.EventStartsAt,
			EventStatus: r.EventStatus, TierName: r.TierName, VenueName: r.VenueName, VenueCity: r.VenueCity,
		}
	}
	return out, total, nil
}

func validate(in Input) error {
	fields := map[string]string{}
	if in.TierID <= 0 {
		fields[FieldTier] = CodeRequired
	}
	if in.Quantity < MinQuantity || in.Quantity > MaxQuantity {
		fields[FieldQuantity] = CodeRange
	}
	if in.Key == uuid.Nil {
		fields[FieldKey] = CodeRequired
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func pgKey(k uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: k, Valid: true}
}
