-- WOPI's own cache mapping for rendered Collabora document previews.
-- Exactly one current mapping per source file: which private preview file
-- currently represents it, and the source updatedDate it was rendered from.
-- No status, TTL, retry, or cross-database foreign key — see data-model.md
-- in the 059-collabora-preview-streaming workspace spec.
CREATE TABLE document_preview_cache (
    source_file_id      TEXT PRIMARY KEY,
    preview_file_id      TEXT NOT NULL,
    source_updated_date TIMESTAMPTZ NOT NULL
);
