env "nagi" {
  src = "ent://schema/nagi"
  dev = "docker://postgres/17/nagi_atlas_dev?search_path=public"
  migration {
    dir = "file://migrations/nagi"
    format = golang-migrate
    exclude = ["schema_migrations", "memory_consent_grants.memory_consent_grant_bound_proposal[type=fk]", "message_jobs.message_jobs_bound_incoming[type=fk]", "group_topics.group_topics_conversation_fk[type=fk]", "group_topic_routes.group_topic_routes_inbound_fk[type=fk]", "group_topic_route_candidates.group_topic_candidates_route_fk[type=fk]", "group_topic_route_candidates.group_topic_candidates_topic_fk[type=fk]", "group_topic_inputs.group_topic_inputs_route_fk[type=fk]", "group_topic_inputs.group_topic_inputs_topic_fk[type=fk]"]
  }
}
