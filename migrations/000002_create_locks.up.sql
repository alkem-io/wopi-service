CREATE TABLE locks (
    id         UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id    VARCHAR(255)  NOT NULL,
    lock_id    VARCHAR(1024) NOT NULL,
    expires_at TIMESTAMPTZ   NOT NULL,
    created_at TIMESTAMPTZ   NOT NULL DEFAULT now(),

    CONSTRAINT uq_locks_file_id UNIQUE (file_id)
);

CREATE INDEX idx_locks_expires_at ON locks (expires_at);
