CREATE TABLE access_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    token      VARCHAR(128) NOT NULL,
    file_id    VARCHAR(255) NOT NULL,
    actor_id   VARCHAR(255) NOT NULL,
    permissions VARCHAR(50) NOT NULL,
    expires_at TIMESTAMPTZ  NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT uq_access_tokens_token UNIQUE (token)
);

CREATE INDEX idx_access_tokens_file_id    ON access_tokens (file_id);
CREATE INDEX idx_access_tokens_expires_at ON access_tokens (expires_at);
