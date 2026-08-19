-- Add gender and is_leader to users table
ALTER TABLE users ADD COLUMN IF NOT EXISTS gender VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_leader BOOLEAN NOT NULL DEFAULT false;
