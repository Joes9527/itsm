-- Migration: 20260811_end_user_ticket_category_read
-- Description: Grant ticket_category:read to end_user role on all tenants.
--              The service catalog page uses ticket categories (L1/L2/L3 tree)
--              as its data source, but end_user only had service_catalog:read.
--              This caused the service catalog to appear empty for end users.
-- Context: Bug fix — Julian@dawnpro.onmicrosoft.com logged in, redirected to
--          服务目录, but saw no content because GET /api/v1/ticket-categories
--          returned 403.

-- Grant ticket_category:read to end_user role (tenant-scoped)
INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'end_user'
  AND p.code = 'ticket_category:read'
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp
    WHERE rp.role_id = r.id AND rp.permission_id = p.id AND rp.tenant_id = r.tenant_id
  );
