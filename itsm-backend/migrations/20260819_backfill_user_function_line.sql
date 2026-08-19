-- Backfill users.function_line from ehr-data.xlsx (sheet "person", column depart_line),
-- matched on username == employee_id. Source CSV extracted from the xlsx via openpyxl
-- (25,791 rows with both employee_id and depart_line, zero duplicate employee_ids).
-- Matched 7,775 of 7,826 users; the ~51 unmatched are the seed admin account plus HR
-- records marked "(已删)"/deleted or otherwise absent from the source sheet.
--
-- To re-run: regenerate the CSV from the source file and adjust the path below.
--   python3 -c "
--   import openpyxl, csv
--   wb = openpyxl.load_workbook('<path-to-ehr-data.xlsx>', read_only=True, data_only=True)
--   ws = wb['person']
--   with open('/tmp/employee_function_line.csv', 'w', newline='', encoding='utf-8') as f:
--       writer = csv.writer(f)
--       for i, row in enumerate(ws.iter_rows(values_only=True)):
--           if i == 0: continue
--           emp_id, depart_line = row[1], row[11]
--           if emp_id and depart_line:
--               writer.writerow([str(emp_id).strip(), str(depart_line).strip()])
--   "

CREATE TEMP TABLE hr_function_line (employee_id text, depart_line text);
\copy hr_function_line FROM '/tmp/employee_function_line.csv' WITH (FORMAT csv);

UPDATE users u
SET function_line = h.depart_line
FROM hr_function_line h
WHERE u.username = h.employee_id;
