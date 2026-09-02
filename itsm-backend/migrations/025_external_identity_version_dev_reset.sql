ALTER TABLE external_identities
    DROP CONSTRAINT IF EXISTS external_identities_version_positive;
ALTER TABLE external_identities
    DROP COLUMN IF EXISTS version;
