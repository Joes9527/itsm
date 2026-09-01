-- Development-only reset. There is no historical-data compatibility path;
-- reassert the authoritative target schema idempotently.
ALTER TABLE incidents DROP COLUMN IF EXISTS title;
ALTER TABLE incidents DROP COLUMN IF EXISTS description;
ALTER TABLE incidents DROP COLUMN IF EXISTS status;
ALTER TABLE incidents DROP COLUMN IF EXISTS priority;
ALTER TABLE incidents ALTER COLUMN work_item_id SET NOT NULL;

ALTER TABLE problems DROP COLUMN IF EXISTS title;
ALTER TABLE problems DROP COLUMN IF EXISTS description;
ALTER TABLE problems DROP COLUMN IF EXISTS status;
ALTER TABLE problems DROP COLUMN IF EXISTS priority;
ALTER TABLE problems ALTER COLUMN work_item_id SET NOT NULL;

ALTER TABLE changes DROP COLUMN IF EXISTS title;
ALTER TABLE changes DROP COLUMN IF EXISTS description;
ALTER TABLE changes DROP COLUMN IF EXISTS status;
ALTER TABLE changes DROP COLUMN IF EXISTS priority;
ALTER TABLE changes ALTER COLUMN work_item_id SET NOT NULL;
