-- Migration: 20260814_knowledge_review_fields
-- Description: 知识库文章审核状态字段
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS review_status VARCHAR(20) DEFAULT 'draft';
ALTER TABLE knowledge_articles ADD COLUMN IF NOT EXISTS review_comment TEXT DEFAULT '';
