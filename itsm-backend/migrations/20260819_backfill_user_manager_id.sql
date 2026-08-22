-- Backfill users.manager_id from ehr-data.xlsx (sheet "person", column direct_supervisor,
-- format "姓名:工号"), matched on username == employee_id, with the supervisor's 工号
-- resolved to their own users.id via a second join. Source CSV extracted via openpyxl
-- (11,928 rows with a clean single "name:id" value; skipped 1 row with dual supervisors
-- separated by "、" and 29 rows where the field held only a name with no employee ID).
-- Matched 7,076 users (both the employee and their supervisor had to already exist in our
-- 7,826-user table).
--
-- To re-run: regenerate the CSV from the source file and adjust the path below.
--   python3 -c "
--   import openpyxl, csv
--   wb = openpyxl.load_workbook('<path-to-ehr-data.xlsx>', read_only=True, data_only=True)
--   ws = wb['person']
--   with open('/tmp/employee_manager.csv', 'w', newline='', encoding='utf-8') as f:
--       writer = csv.writer(f)
--       for i, row in enumerate(ws.iter_rows(values_only=True)):
--           if i == 0: continue
--           emp_id, sup = row[1], row[13]
--           if not emp_id or not sup: continue
--           sup = str(sup).strip()
--           if '、' in sup or ',' in sup: continue
--           parts = sup.split(':')
--           if len(parts) != 2 or not parts[1].strip(): continue
--           writer.writerow([str(emp_id).strip(), parts[1].strip()])
--   "

CREATE TEMP TABLE hr_manager (employee_id text, manager_employee_id text);
\copy hr_manager FROM '/tmp/employee_manager.csv' WITH (FORMAT csv);

UPDATE users u
SET manager_id = mu.id
FROM hr_manager h
JOIN users mu ON mu.username = h.manager_employee_id
WHERE u.username = h.employee_id;
