-- Migration: 20260807_service_request_delegates_to_ticket
-- Description: ServiceRequest no longer carries its own status/approval-progress
--             columns. Status/workflow/approval are delegated to the linked Ticket
--             (service_requests.ticket_id, 1:1) via the existing BPMN
--             process_approval_decision mechanism. Drops the hardcoded 3-level
--             manager->IT->security approval table (service_request_approvals)
--             entirely.
-- Target: production and existing deployments
-- Date: 2026-08-07
--
-- NOTE: This repo's actual executed migration stream is the Go registry in
-- itsm-backend/migration/migrations.go (RegisteredMigrations / GetMigrationSQL), applied via
-- internal/bootstrap/app.go at server start and itsm-backend/cmd/migrate (build tag `migrate`).
-- Loose .sql files under itsm-backend/migrations/ are NOT auto-discovered or executed by any
-- Go code in this repo. This file is kept for documentation/history consistency with the
-- existing convention in this directory; the SQL below is registered verbatim as migration
-- version "013_service_request_delegates_to_ticket" in migration/migrations.go, which is what
-- actually runs.

ALTER TABLE service_requests DROP COLUMN IF EXISTS status;
ALTER TABLE service_requests DROP COLUMN IF EXISTS title;
ALTER TABLE service_requests DROP COLUMN IF EXISTS reason;
ALTER TABLE service_requests DROP COLUMN IF EXISTS current_level;
ALTER TABLE service_requests DROP COLUMN IF EXISTS total_levels;
ALTER TABLE service_requests DROP COLUMN IF EXISTS current_approver;
ALTER TABLE service_requests DROP COLUMN IF EXISTS approved_at;
ALTER TABLE service_requests DROP COLUMN IF EXISTS approver_comment;
ALTER TABLE service_requests DROP COLUMN IF EXISTS approval_history;
DROP TABLE IF EXISTS service_request_approvals;
-- 旧的、归属在 entity_type='service_request' 下的自定义字段值是测试数据，直接清掉，
-- 新提交都会落在 entity_type='ticket' 下（见 handlers/service_request/service.go Create）。
DELETE FROM field_values WHERE entity_type = 'service_request';
