package users

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	uniqueViolation   = "23505"
	emailConstraint   = "users_email_key"
	signInMaxAttempts = 3
)

var (
	ErrInvalidProfile   = errors.New("google profile has no subject or email")
	ErrEmailNotVerified = errors.New("google account email is not verified")
	ErrEmailTaken       = errors.New("email belongs to another account")
)

type GoogleProfile struct {
	Sub           string
	Email         string
	EmailVerified bool
	Name          string
}

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) Get(ctx context.Context, id int64) (dbgen.User, error) {
	return s.q.GetUser(ctx, id)
}

func (s *Service) SignInWithGoogle(ctx context.Context, p GoogleProfile) (dbgen.User, error) {
	p.Sub = strings.TrimSpace(p.Sub)
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	if p.Sub == "" || p.Email == "" {
		return dbgen.User{}, ErrInvalidProfile
	}
	if !p.EmailVerified {
		return dbgen.User{}, ErrEmailNotVerified
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		p.Name, _, _ = strings.Cut(p.Email, "@")
	}

	for attempt := 1; ; attempt++ {
		u, err := s.signIn(ctx, p)
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
			return u, err
		}
		if attempt == signInMaxAttempts {
			if pgErr.ConstraintName == emailConstraint {
				return dbgen.User{}, ErrEmailTaken
			}
			return dbgen.User{}, fmt.Errorf("sign in with google: %w", err)
		}
	}
}

func (s *Service) signIn(ctx context.Context, p GoogleProfile) (dbgen.User, error) {
	sub := pgtype.Text{String: p.Sub, Valid: true}
	u, err := s.q.UpdateGoogleUser(ctx, dbgen.UpdateGoogleUserParams{Email: p.Email, Name: p.Name, GoogleSub: sub})
	if !errors.Is(err, pgx.ErrNoRows) {
		return u, err
	}
	u, err = s.q.LinkGoogleAccount(ctx, dbgen.LinkGoogleAccountParams{GoogleSub: sub, Name: p.Name, Email: p.Email})
	if !errors.Is(err, pgx.ErrNoRows) {
		return u, err
	}
	return s.q.CreateUser(ctx, dbgen.CreateUserParams{Email: p.Email, Name: p.Name, GoogleSub: sub})
}
