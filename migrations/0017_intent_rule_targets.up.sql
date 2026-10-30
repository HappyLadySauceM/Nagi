ALTER TABLE intent_rulesets ADD COLUMN targets JSONB NOT NULL DEFAULT '[]'::jsonb;
UPDATE intent_rulesets SET targets=jsonb_build_array(jsonb_build_object('kind',scope_type,'id',scope_id)) WHERE scope_id>0;
