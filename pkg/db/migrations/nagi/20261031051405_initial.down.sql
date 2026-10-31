-- Destructive rollback: drops all business data. Review explicitly; restore from backup when data must survive.
-- 破坏性回滚：删除全部业务数据，必须明确评审；需要保留数据时从备份恢复。
-- reverse: modify "scope_policies" table
ALTER TABLE "scope_policies" DROP CONSTRAINT "scope_policies_bot_accounts_scope_policies_bot_id";
-- reverse: modify "qq_groups" table
ALTER TABLE "qq_groups" DROP CONSTRAINT "qq_groups_bot_accounts_qq_groups_bot_id";
-- reverse: modify "messages" table
ALTER TABLE "messages" DROP CONSTRAINT "messages_conversations_messages_conversation_id", DROP CONSTRAINT "messages_bot_accounts_messages_bot_id", DROP CONSTRAINT "messages_agent_outbound_messages_messages_outbound_id";
-- reverse: modify "message_jobs" table
ALTER TABLE "message_jobs" DROP CONSTRAINT "message_jobs_messages_message_jobs_incoming_message_id";
-- reverse: modify "memories" table
ALTER TABLE "memories" DROP CONSTRAINT "memories_messages_memories_source_message_id", DROP CONSTRAINT "memories_bot_accounts_memories_bot_id";
-- reverse: modify "intent_rule_entries" table
ALTER TABLE "intent_rule_entries" DROP CONSTRAINT "intent_rule_entries_intent_rulesets_intent_rule_entries_ruleset";
-- reverse: modify "intent_rule_activations" table
ALTER TABLE "intent_rule_activations" DROP CONSTRAINT "intent_rule_activations_intent_rulesets_intent_rule_activations";
-- reverse: modify "intent_decisions" table
ALTER TABLE "intent_decisions" DROP CONSTRAINT "intent_decisions_message_jobs_intent_decisions_job_id";
-- reverse: modify "intent_decision_steps" table
ALTER TABLE "intent_decision_steps" DROP CONSTRAINT "intent_decision_steps_intent_decisions_intent_decision_steps_de";
-- reverse: modify "group_members" table
ALTER TABLE "group_members" DROP CONSTRAINT "group_members_bot_accounts_group_members_bot_id";
-- reverse: modify "conversations" table
ALTER TABLE "conversations" DROP CONSTRAINT "conversations_bot_accounts_conversations_bot_id";
-- reverse: modify "contacts" table
ALTER TABLE "contacts" DROP CONSTRAINT "contacts_bot_accounts_contacts_bot_id";
-- reverse: modify "agent_run_budgets" table
ALTER TABLE "agent_run_budgets" DROP CONSTRAINT "agent_run_budgets_message_jobs_agent_run_budgets_job_id";
-- reverse: modify "agent_outbound_messages" table
ALTER TABLE "agent_outbound_messages" DROP CONSTRAINT "agent_outbound_messages_message_jobs_agent_outbound_messages_jo", DROP CONSTRAINT "agent_outbound_messages_conversations_agent_outbound_messages_c", DROP CONSTRAINT "agent_outbound_messages_bot_accounts_agent_outbound_messages_bo";
-- reverse: create "system_settings" table
DROP TABLE "system_settings";
-- reverse: create index "scope_policies_business_key" to table: "scope_policies"
DROP INDEX "scope_policies_business_key";
-- reverse: create "scope_policies" table
DROP TABLE "scope_policies";
-- reverse: create index "qq_groups_business_key" to table: "qq_groups"
DROP INDEX "qq_groups_business_key";
-- reverse: create "qq_groups" table
DROP TABLE "qq_groups";
-- reverse: create index "messages_outbound_unique" to table: "messages"
DROP INDEX "messages_outbound_unique";
-- reverse: create index "messages_incoming_event_uniq" to table: "messages"
DROP INDEX "messages_incoming_event_uniq";
-- reverse: create index "messages_created_at" to table: "messages"
DROP INDEX "messages_created_at";
-- reverse: create index "messages_conversation_timeline" to table: "messages"
DROP INDEX "messages_conversation_timeline";
-- reverse: create "messages" table
DROP TABLE "messages";
-- reverse: create index "message_jobs_ready" to table: "message_jobs"
DROP INDEX "message_jobs_ready";
-- reverse: create index "message_jobs_incoming_message_id_key" to table: "message_jobs"
DROP INDEX "message_jobs_incoming_message_id_key";
-- reverse: create index "message_jobs_expired" to table: "message_jobs"
DROP INDEX "message_jobs_expired";
-- reverse: create "message_jobs" table
DROP TABLE "message_jobs";
-- reverse: create index "memories_visibility" to table: "memories"
DROP INDEX "memories_visibility";
-- reverse: create "memories" table
DROP TABLE "memories";
-- reverse: create index "intent_rulesets_scope" to table: "intent_rulesets"
DROP INDEX "intent_rulesets_scope";
-- reverse: create index "intent_rulesets_bot_id_scope_type_scope_id_version_key" to table: "intent_rulesets"
DROP INDEX "intent_rulesets_bot_id_scope_type_scope_id_version_key";
-- reverse: create "intent_rulesets" table
DROP TABLE "intent_rulesets";
-- reverse: create index "intent_rule_entries_ruleset_id_priority_key" to table: "intent_rule_entries"
DROP INDEX "intent_rule_entries_ruleset_id_priority_key";
-- reverse: create "intent_rule_entries" table
DROP TABLE "intent_rule_entries";
-- reverse: create index "intent_rule_activations_business_key" to table: "intent_rule_activations"
DROP INDEX "intent_rule_activations_business_key";
-- reverse: create "intent_rule_activations" table
DROP TABLE "intent_rule_activations";
-- reverse: create index "intent_decisions_job" to table: "intent_decisions"
DROP INDEX "intent_decisions_job";
-- reverse: create "intent_decisions" table
DROP TABLE "intent_decisions";
-- reverse: create index "intent_decision_steps_decision_id_step_index_key" to table: "intent_decision_steps"
DROP INDEX "intent_decision_steps_decision_id_step_index_key";
-- reverse: create index "intent_decision_steps_decision" to table: "intent_decision_steps"
DROP INDEX "intent_decision_steps_decision";
-- reverse: create "intent_decision_steps" table
DROP TABLE "intent_decision_steps";
-- reverse: create index "group_members_business_key" to table: "group_members"
DROP INDEX "group_members_business_key";
-- reverse: create "group_members" table
DROP TABLE "group_members";
-- reverse: create index "conversations_bot_id_kind_target_id_key" to table: "conversations"
DROP INDEX "conversations_bot_id_kind_target_id_key";
-- reverse: create "conversations" table
DROP TABLE "conversations";
-- reverse: create index "contacts_business_key" to table: "contacts"
DROP INDEX "contacts_business_key";
-- reverse: create "contacts" table
DROP TABLE "contacts";
-- reverse: create "bot_accounts" table
DROP TABLE "bot_accounts";
-- reverse: create index "agent_sending_policies_business_key" to table: "agent_sending_policies"
DROP INDEX "agent_sending_policies_business_key";
-- reverse: create "agent_sending_policies" table
DROP TABLE "agent_sending_policies";
-- reverse: create "agent_run_budgets" table
DROP TABLE "agent_run_budgets";
-- reverse: create index "agent_outbound_messages_job_id_sequence_key" to table: "agent_outbound_messages"
DROP INDEX "agent_outbound_messages_job_id_sequence_key";
-- reverse: create index "agent_outbound_job_tool_call_uidx" to table: "agent_outbound_messages"
DROP INDEX "agent_outbound_job_tool_call_uidx";
-- reverse: create index "agent_outbound_conversation_timeline" to table: "agent_outbound_messages"
DROP INDEX "agent_outbound_conversation_timeline";
-- reverse: create "agent_outbound_messages" table
DROP TABLE "agent_outbound_messages";
-- reverse: create "admin_audit_logs" table
DROP TABLE "admin_audit_logs";

DELETE FROM schema_migrations WHERE version=20261031051405;
