ALTER TABLE message_jobs ADD COLUMN delivery_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE message_jobs ADD COLUMN delivery_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE message_jobs ADD COLUMN denied_count INTEGER NOT NULL DEFAULT 0 CHECK (denied_count >= 0);
