-- Migration: 20260810_service_catalog_itsm_type
-- Description: Add itsm_type column to service_catalogs to support ITSM-type-based 
--              approval routing (Request/Incident/Change).
--              Defaults to "Request" for backward compatibility.
ALTER TABLE service_catalogs ADD COLUMN IF NOT EXISTS itsm_type VARCHAR(20) DEFAULT 'Request';
COMMENT ON COLUMN service_catalogs.itsm_type IS 'ITSM类型: Request|Incident|Change，决定审批路由';
