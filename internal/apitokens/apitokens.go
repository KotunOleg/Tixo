package apitokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	FieldName = "name"

	CodeRequired = "required"
	CodeLength   = "length"

	nameMaxLen = 100

	BearerPrefix = "Bearer "
)

var (
	ErrNotFound      = errors.New("api token not found")
	ErrInvalidToken  = errors.New("invalid api token")
	ErrInvalidBearer = errors.New("invalid bearer header")
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
	return "invalid api token: " + strings.Join(parts, ", ")
}

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) Issue(ctx context.Context, userID int64, name string) (raw string, token dbgen.ApiToken, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", dbgen.ApiToken{}, &ValidationError{Fields: map[string]string{FieldName: CodeRequired}}
	}
	if utf8.RuneCountInString(name) > nameMaxLen {
		return "", dbgen.ApiToken{}, &ValidationError{Fields: map[string]string{FieldName: CodeLength}}
	}
	raw = rand.Text()
	token, err = s.q.CreateAPIToken(ctx, dbgen.CreateAPITokenParams{UserID: userID, Name: name, TokenHash: hash(raw)})
	if err != nil {
		return "", dbgen.ApiToken{}, fmt.Errorf("issue api token: %w", err)
	}
	return raw, token, nil
}

func (s *Service) List(ctx context.Context, userID int64) ([]dbgen.ApiToken, error) {
	return s.q.ListAPITokensForUser(ctx, userID)
}

func (s *Service) Resolve(ctx context.Context, raw string) (dbgen.User, error) {
	if raw == "" {
		return dbgen.User{}, ErrInvalidToken
	}
	u, err := s.q.GetUserByAPITokenHash(ctx, hash(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.User{}, ErrInvalidToken
	}
	if err != nil {
		return dbgen.User{}, fmt.Errorf("resolve api token: %w", err)
	}
	if err := s.q.TouchAPIToken(ctx, hash(raw)); err != nil {
		return dbgen.User{}, fmt.Errorf("touch api token: %w", err)
	}
	return u, nil
}

func (s *Service) Revoke(ctx context.Context, userID, id int64) error {
	n, err := s.q.DeleteAPIToken(ctx, dbgen.DeleteAPITokenParams{ID: id, UserID: userID})
	if err != nil {
		return fmt.Errorf("revoke api token: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func ParseBearer(header string) (string, error) {
	if !strings.HasPrefix(header, BearerPrefix) {
		return "", ErrInvalidBearer
	}
	raw := strings.TrimSpace(strings.TrimPrefix(header, BearerPrefix))
	if raw == "" {
		return "", ErrInvalidBearer
	}
	return raw, nil
}

func hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
