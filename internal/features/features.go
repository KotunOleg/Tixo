package features

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	SalesAnalytics = "sales_analytics"

	foreignKeyViolation = "23503"
)

var ErrUnknown = errors.New("unknown feature flag or organizer")

type State struct {
	Name          string
	EnabledForAll bool
	ByPlan        bool
	ByOverride    bool
}

func (s State) Enabled() bool {
	return s.EnabledForAll || s.ByPlan || s.ByOverride
}

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) Enabled(ctx context.Context, organizerID int64, name string) (bool, error) {
	on, err := s.q.FeatureEnabledForOrganizer(ctx, dbgen.FeatureEnabledForOrganizerParams{Name: name, OrganizerID: organizerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	if err != nil {
		return false, fmt.Errorf("feature %s: %w", name, err)
	}
	return on, nil
}

func (s *Service) Enable(ctx context.Context, organizerID int64, name string) error {
	err := s.q.EnableFeatureForOrganizer(ctx, dbgen.EnableFeatureForOrganizerParams{Name: name, OrganizerID: organizerID})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	if err != nil {
		return fmt.Errorf("enable %s: %w", name, err)
	}
	return nil
}

func (s *Service) Disable(ctx context.Context, organizerID int64, name string) error {
	if _, err := s.Enabled(ctx, organizerID, name); err != nil {
		return err
	}
	if _, err := s.q.DisableFeatureForOrganizer(ctx, dbgen.DisableFeatureForOrganizerParams{Name: name, OrganizerID: organizerID}); err != nil {
		return fmt.Errorf("disable %s: %w", name, err)
	}
	return nil
}

func (s *Service) List(ctx context.Context, organizerID int64) ([]State, error) {
	rows, err := s.q.ListFeaturesForOrganizer(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	out := make([]State, len(rows))
	for i, r := range rows {
		out[i] = State{Name: r.Name, EnabledForAll: r.EnabledForAll, ByPlan: r.ByPlan, ByOverride: r.ByOverride}
	}
	return out, nil
}
