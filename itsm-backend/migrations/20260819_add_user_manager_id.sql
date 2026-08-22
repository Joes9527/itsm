-- Add manager_id to users table: personal-level direct-report supervisor (from HR source
-- ehr-data.xlsx person.direct_supervisor, format "姓名:工号"), distinct from
-- departments.manager_id which is the department-level manager. No FK constraint, matching
-- the existing convention for departments.manager_id in this schema.
ALTER TABLE users ADD COLUMN IF NOT EXISTS manager_id BIGINT;
