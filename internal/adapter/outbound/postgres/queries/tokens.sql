-- name: InsertToken :exec
INSERT INTO access_tokens (id, token, file_id, actor_id, actor_name, permissions, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: FindByToken :one
SELECT id, token, file_id, actor_id, actor_name, permissions, expires_at, created_at
FROM access_tokens
WHERE token = $1;

-- name: DeleteTokenByID :exec
DELETE FROM access_tokens WHERE id = $1;

-- name: DeleteExpiredTokens :execrows
DELETE FROM access_tokens WHERE expires_at < now();
