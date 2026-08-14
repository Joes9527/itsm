-- Migration: 20260813_ticket_conversation_id
-- Description: Add conversation_id to tickets for email reply threading
--              (Graph conversationId), used to recognize user replies and
--              append them as comments instead of creating duplicate tickets.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS conversation_id VARCHAR(255);

-- Composite unique index on (tenant_id, conversation_id) for fast reply lookup.
-- PostgreSQL unique indexes allow multiple NULLs, so non-email tickets
-- (conversation_id IS NULL) are unaffected.
CREATE UNIQUE INDEX IF NOT EXISTS tickets_tenant_id_conversation_id
    ON tickets (tenant_id, conversation_id);
