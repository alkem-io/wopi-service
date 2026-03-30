-- name: InsertSession :exec
INSERT INTO wopi_sessions (id, file_id, actor_id, token_id, created_at)
VALUES ($1, $2, $3, $4, $5);

-- name: FindSessionsByFileID :many
SELECT id, file_id, actor_id, token_id, created_at
FROM wopi_sessions
WHERE file_id = $1;

-- name: DeleteSessionByTokenID :exec
DELETE FROM wopi_sessions WHERE token_id = $1;
