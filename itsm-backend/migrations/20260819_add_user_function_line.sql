-- Add function_line to users table: a horizontal functional-reporting-line tag from HR
-- source data (ehr-data.xlsx person.depart_line), independent of department_id / the formal
-- org tree. Same functional line can span people scattered across different legal entities.
ALTER TABLE users ADD COLUMN IF NOT EXISTS function_line VARCHAR(255);
