-- Strip the leading literal single-quote character from users.phone left over from the
-- org/personnel data reimport (classic Excel "force text" artifact on a column that looks
-- numeric — the leading apostrophe wasn't stripped before import). Confirmed 100% consistent
-- before running: every non-empty phone value starts with exactly one literal '.
UPDATE users
SET phone = substring(phone FROM 2)
WHERE phone LIKE '''%';
