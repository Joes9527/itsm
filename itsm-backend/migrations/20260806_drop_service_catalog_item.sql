-- Migration: 20260806_drop_service_catalog_item
-- Description: Drop the legacy ServiceCatalogItem table and the never-written
--             service_catalogs.form_schema column. These backed the orphaned
--             ServiceCatalogItemService/ServiceCatalogService/ServiceRequestService +
--             ServiceController branch (controller/service_controller.go and its
--             /api/v1/services/catalogs*, /api/v1/services/requests* routes), which had
--             zero real HTTP callers (frontend, itsm-cli, itsm-agent, tests) and has been
--             removed. Superseded by handlers/service_catalog + handlers/service_request.
-- Target: production and existing deployments
-- Date: 2026-08-06
--
-- NOTE: This repo's actual executed migration stream is the Go registry in
-- itsm-backend/migration/migrations.go (RegisteredMigrations / GetMigrationSQL), applied via
-- internal/bootstrap/app.go at server start and itsm-backend/cmd/migrate (build tag `migrate`).
-- Loose .sql files under itsm-backend/migrations/ are NOT auto-discovered or executed by any
-- Go code in this repo (verified: no ReadDir/Glob over this directory, no deploy script runs
-- psql over these files) -- confirmed during Task 10 investigation. This file is kept for
-- documentation/history consistency with the existing convention in this directory; the SQL
-- below is registered verbatim as migration version "012_drop_service_catalog_item" in
-- migration/migrations.go, which is what actually runs.

DROP TABLE IF EXISTS service_catalog_items;
ALTER TABLE service_catalogs DROP COLUMN IF EXISTS form_schema;
