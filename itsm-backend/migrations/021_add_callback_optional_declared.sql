ALTER TABLE process_callback_outboxes
    ADD COLUMN IF NOT EXISTS optional_declared boolean NOT NULL DEFAULT false;
