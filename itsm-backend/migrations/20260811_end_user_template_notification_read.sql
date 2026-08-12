-- Migration: 20260811_end_user_template_notification_read
-- Description: Grant ticket_template:read and notification:read to end_user role.
--              The ticket create page loads templates by category, and the
--              notification bell polls /api/v1/notifications — both returned
--              403 for end_user because the role lacked these permissions.

-- Grant ticket_template:read to end_user
INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'end_user'
  AND p.code = 'ticket_template:read'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp
    WHERE rp.role_id = r.id AND rp.permission_id = p.id AND rp.tenant_id = r.tenant_id
  );

-- Grant notification:read to end_user
INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'end_user'
  AND p.code = 'notification:read'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp
    WHERE rp.role_id = r.id AND rp.permission_id = p.id AND rp.tenant_id = r.tenant_id
  );
