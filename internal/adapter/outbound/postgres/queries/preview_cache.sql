-- name: FindPreviewCacheBySourceID :one
SELECT source_file_id, preview_file_id, source_updated_date
FROM document_preview_cache
WHERE source_file_id = $1;

-- name: UpsertPreviewCache :exec
-- preview_file_id and source_updated_date are always written together in
-- one statement: a re-render never updates one without the other.
INSERT INTO document_preview_cache (source_file_id, preview_file_id, source_updated_date)
VALUES ($1, $2, $3)
ON CONFLICT (source_file_id) DO UPDATE
SET preview_file_id = EXCLUDED.preview_file_id,
    source_updated_date = EXCLUDED.source_updated_date;
