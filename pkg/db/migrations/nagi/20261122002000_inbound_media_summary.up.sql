ALTER TABLE "messages" ADD COLUMN "inbound_media_summary" jsonb;

INSERT INTO schema_migrations(version,name) VALUES (20261122002000,'inbound_media_summary');
