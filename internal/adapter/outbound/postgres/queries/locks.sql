-- name: UpsertLock :exec
INSERT INTO locks (id, file_id, lock_id, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (file_id) DO UPDATE
SET lock_id = EXCLUDED.lock_id, expires_at = EXCLUDED.expires_at;

-- name: FindLockByFileID :one
SELECT id, file_id, lock_id, expires_at, created_at
FROM locks
WHERE file_id = $1;

-- name: UpdateLockExpiry :exec
UPDATE locks SET expires_at = $2 WHERE file_id = $1;

-- name: UpdateLockIDAndExpiry :exec
UPDATE locks SET lock_id = $2, expires_at = $3 WHERE file_id = $1;

-- name: DeleteLockByFileID :exec
DELETE FROM locks WHERE file_id = $1;

-- name: DeleteExpiredLocks :execrows
DELETE FROM locks WHERE expires_at < now();
