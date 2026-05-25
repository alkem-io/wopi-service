-- Recreate wopi_sessions (originally from 000003) so the migration is
-- reversible. The table is left empty; rows that existed before the
-- drop are gone and not recoverable.
CREATE TABLE wopi_sessions (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id    VARCHAR(255) NOT NULL,
    actor_id   VARCHAR(255) NOT NULL,
    token_id   UUID         NOT NULL REFERENCES access_tokens(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_wopi_sessions_file_id  ON wopi_sessions (file_id);
CREATE INDEX idx_wopi_sessions_token_id ON wopi_sessions (token_id);
