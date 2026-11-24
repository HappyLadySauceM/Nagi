package contracts

const GroupInputOrderJoins = `
 LEFT JOIN group_topic_inputs binding ON binding.route_id=m.id AND binding.conversation_id=m.conversation_id
 LEFT JOIN LATERAL (
  SELECT max(root.route_id) AS id FROM group_topic_inputs root
  WHERE root.topic_id=binding.topic_id AND root.conversation_id=binding.conversation_id
    AND binding.action IN ('contribution','corrected','replaced')
    AND root.version<binding.version AND root.action IN ('opened','waiting','follow_up')
 ) origin ON true`

const GroupInputOrderKey = `COALESCE(origin.id,m.id),m.id`

const GroupPendingHead = `
 SELECT j.id,j.delivery_continuation,j.next_run_at,m.id AS input
 FROM messages m JOIN message_jobs j ON j.incoming_message_id=m.id` + GroupInputOrderJoins + `
 WHERE m.conversation_id=c.id AND m.direction='incoming' AND j.status='pending'
 ORDER BY ` + GroupInputOrderKey + ` LIMIT 1`
