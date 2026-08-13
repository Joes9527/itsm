-- Migration: 20260813_sla_definition_columns
-- Description: SLA 重新设计新增的列 (Phase 2/3)
ALTER TABLE sla_definitions ADD COLUMN IF NOT EXISTS category_ids JSONB DEFAULT '[]';
ALTER TABLE sla_definitions ADD COLUMN IF NOT EXISTS exclude_weekends BOOLEAN DEFAULT false;
ALTER TABLE sla_definitions ADD COLUMN IF NOT EXISTS exclude_holidays BOOLEAN DEFAULT false;
