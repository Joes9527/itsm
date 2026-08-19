-- Fix 6 top-level departments that should be children of "公司架构" (code=1) per the
-- source org sheet's org_code/parent_code relationship, but got imported as false roots.
--
-- Root cause: these are the only org_code/parent_code values in the entire source file
-- that are purely numeric (1, 11, 12, 14, 15, 16, 17) — Excel/openpyxl reads such cells
-- back as Python int rather than str, unlike the far more common alphanumeric codes
-- (e.g. "11D010G"/"11D01") which are unambiguously strings. The import's parent lookup
-- apparently compared without normalizing types, so these int-typed codes never matched
-- and silently fell back to parent_id = NULL (root). Confirmed by an exhaustive join of
-- all 7,961 source org rows against departments.code/parent_id — this is the complete
-- set of discrepancies (7 total: 1 is code=1 itself, a legitimate self-referencing root
-- in the source data, correctly left alone; the other 6 are the real bug, fixed here).
UPDATE departments
SET parent_id = (SELECT id FROM departments WHERE code = '1')
WHERE code IN ('11', '12', '14', '15', '16', '17');
