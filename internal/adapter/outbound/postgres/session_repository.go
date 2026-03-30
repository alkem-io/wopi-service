package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alkem-io/wopi-service/internal/adapter/outbound/postgres/generated"
	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// SessionRepository implements port.SessionRepository using PostgreSQL.
type SessionRepository struct {
	pool *pgxpool.Pool
}

// NewSessionRepository creates a new SessionRepository.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create stores a new WOPI session.
func (r *SessionRepository) Create(ctx context.Context, session *model.WOPISession) error {
	q := generated.New(r.pool)
	return q.InsertSession(ctx, generated.InsertSessionParams{
		ID:        uuidToPgtype(session.ID),
		FileID:    session.FileID,
		ActorID:   session.ActorID,
		TokenID:   uuidToPgtype(session.TokenID),
		CreatedAt: timestamptzFromTime(session.CreatedAt),
	})
}

// FindByFileID retrieves all active sessions for a file.
func (r *SessionRepository) FindByFileID(ctx context.Context, fileID string) ([]model.WOPISession, error) {
	q := generated.New(r.pool)
	rows, err := q.FindSessionsByFileID(ctx, fileID)
	if err != nil {
		return nil, err
	}
	sessions := make([]model.WOPISession, len(rows))
	for i, row := range rows {
		sessions[i] = model.WOPISession{
			ID:        pgTypeToUUID(row.ID),
			FileID:    row.FileID,
			ActorID:   row.ActorID,
			TokenID:   pgTypeToUUID(row.TokenID),
			CreatedAt: row.CreatedAt.Time,
		}
	}
	return sessions, nil
}

// DeleteByTokenID removes sessions associated with a token.
func (r *SessionRepository) DeleteByTokenID(ctx context.Context, tokenID string) error {
	q := generated.New(r.pool)
	uid, err := parseUUID(tokenID)
	if err != nil {
		return err
	}
	return q.DeleteSessionByTokenID(ctx, uid)
}
