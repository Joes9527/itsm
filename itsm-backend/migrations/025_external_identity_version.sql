ALTER TABLE external_identities
    ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1;
ALTER TABLE external_identities
    DROP CONSTRAINT IF EXISTS external_identities_version_positive;
ALTER TABLE external_identities
    ADD CONSTRAINT external_identities_version_positive CHECK (version > 0);
