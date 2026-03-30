package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alkem-io/wopi-service/internal/adapter/outbound/postgres/generated"
	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// TokenRepository implements port.TokenRepository using PostgreSQL.
type TokenRepository struct {
	pool *pgxpool.Pool
}

// NewTokenRepository creates a new TokenRepository.
func NewTokenRepository(pool *pgxpool.Pool) *TokenRepository {
	return &TokenRepository{pool: pool}
}

// Create stores a new access token.
func (r *TokenRepository) Create(ctx context.Context, token *model.AccessToken) error {
	q := generated.New(r.pool)
	return q.InsertToken(ctx, generated.InsertTokenParams{
		ID:          uuidToPgtype(token.ID),
		Token:       token.Token,
		FileID:      token.FileID,
		ActorID:     token.ActorID,
		Permissions: token.Permissions,
		ExpiresAt:   timestamptzFromTime(token.ExpiresAt),
		CreatedAt:   timestamptzFromTime(token.CreatedAt),
	})
}

// FindByToken retrieves a token by its opaque value. Returns nil if not found.
func (r *TokenRepository) FindByToken(ctx context.Context, tokenValue string) (*model.AccessToken, error) {
	q := generated.New(r.pool)
	row, err := q.FindByToken(ctx, tokenValue)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &model.AccessToken{
		ID:          pgTypeToUUID(row.ID),
		Token:       row.Token,
		FileID:      row.FileID,
		ActorID:     row.ActorID,
		Permissions: row.Permissions,
		ExpiresAt:   row.ExpiresAt.Time,
		CreatedAt:   row.CreatedAt.Time,
	}, nil
}

// DeleteByID removes a token by its UUID.
func (r *TokenRepository) DeleteByID(ctx context.Context, id string) error {
	q := generated.New(r.pool)
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return q.DeleteTokenByID(ctx, uid)
}

// DeleteExpired removes all expired tokens and returns the count deleted.
func (r *TokenRepository) DeleteExpired(ctx context.Context) (int64, error) {
	q := generated.New(r.pool)
	return q.DeleteExpiredTokens(ctx)
}
