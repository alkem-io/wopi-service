-- name: UpsertLock :exec
INSERT INTO locks (id, file_id, lock_id, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (file_id) DO UPDATE
SET lock_id = EXCLUDED.lock_id, expires_at = EXCLUDED.expires_at;

-- name: FindLockByFileID :one
SELECT id, file_id, lock_id, expires_at, created_at
FROM locks
WHERE file_id = $1 AND expires_at > now();

-- name: UpdateLockExpiry :execrows
UPDATE locks SET expires_at = $3
WHERE file_id = $1 AND lock_id = $2 AND expires_at > now();

-- name: UpdateLockIDAndExpiry :execrows
UPDATE locks SET lock_id = $3, expires_at = $4
WHERE file_id = $1 AND lock_id = $2 AND expires_at > now();

-- name: DeleteLockByFileID :execrows
DELETE FROM locks WHERE file_id = $1 AND lock_id = $2;

-- name: DeleteExpiredLocks :execrows
DELETE FROM locks WHERE expires_at < now();
