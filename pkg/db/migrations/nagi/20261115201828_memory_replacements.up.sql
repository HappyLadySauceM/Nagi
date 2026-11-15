-- create "memory_replacements" table
CREATE TABLE "memory_replacements" (
  "replacement_id" uuid NOT NULL,
  "bot_id" bigint NOT NULL,
  "new_memory_id" uuid NOT NULL,
  "old_memory_id" uuid NOT NULL,
  "actor" text NOT NULL,
  "reason" text NOT NULL DEFAULT '',
  "idempotency_key" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("replacement_id"),
  CONSTRAINT "memory_replacements_idempotency_key_check" CHECK ((char_length(idempotency_key) >= 1) AND (char_length(idempotency_key) <= 128))
);
-- create index "memory_replacements_idempotency" to table: "memory_replacements"
CREATE UNIQUE INDEX "memory_replacements_idempotency" ON "memory_replacements" ("bot_id", "idempotency_key");
-- create index "memory_replacements_old" to table: "memory_replacements"
CREATE INDEX "memory_replacements_old" ON "memory_replacements" ("old_memory_id");
-- create index "memory_replacements_pair" to table: "memory_replacements"
CREATE UNIQUE INDEX "memory_replacements_pair" ON "memory_replacements" ("new_memory_id", "old_memory_id");

INSERT INTO schema_migrations(version,name) VALUES (20261115201828,'memory_replacements');
