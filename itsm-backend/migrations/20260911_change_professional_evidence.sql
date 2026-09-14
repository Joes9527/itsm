-- Add evidence only. Historical results, reviews and policies remain unknown.
ALTER TABLE changes
 ADD COLUMN IF NOT EXISTS outcome varchar NULL,
 ADD COLUMN IF NOT EXISTS outcome_evidence text NULL,
 ADD COLUMN IF NOT EXISTS assessment_evidence text NULL,
 ADD COLUMN IF NOT EXISTS assessment_digest varchar NULL,
 ADD COLUMN IF NOT EXISTS assessed_by bigint NULL,
 ADD COLUMN IF NOT EXISTS assessed_at timestamptz NULL,
 ADD COLUMN IF NOT EXISTS reviewed_by bigint NULL,
 ADD COLUMN IF NOT EXISTS reviewed_at timestamptz NULL,
 ADD COLUMN IF NOT EXISTS review_evidence text NULL,
 ADD COLUMN IF NOT EXISTS review_digest varchar NULL,
 ADD COLUMN IF NOT EXISTS standard_policy jsonb NULL;
-- standard_change_changes is the existing template relationship column.
