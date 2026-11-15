import { describe, expect, it } from 'vitest'
import {
  toBuiltinRuleCatalog,
  toRuleEntriesRequest,
  toRulesEffective,
  toRuleset,
  toRuleSimulation,
} from '../adapters/rules'

const raw = { ID:1, BotID:2, ScopeType:'private', ScopeID:3, Version:1, Status:'draft', Note:'', Entries:[{ Priority:5, Match:{text_contains:'hello',min_seconds_since_reply:60},Action:{type:'silent',reason_code:'quiet'}}] }
const builtinCatalog = {
  Version: 'builtin-v1',
  Rules: [
    { RuleID: 'closing', Order: 10, Title: '告别结束', Condition: 'intent=closing', Action: 'silent', Editable: true, Source: 'builtin' },
    { RuleID: 'directed_to_bot', Order: 50, Title: '指向机器人', Condition: 'directed_to_bot >= Continuation', Action: 'reply', Editable: true, Source: 'builtin' },
  ],
}

describe('rule wire boundary',()=>{
 it('reads the backend-owned builtin rule catalog without duplicating definitions', () => {
  const catalog = toBuiltinRuleCatalog(builtinCatalog)
  expect(catalog.version).toBe('builtin-v1')
  expect(catalog.rules).toHaveLength(2)
  expect(catalog.rules[0]).toEqual({
    ruleId: 'closing', order: 10, title: '告别结束', condition: 'intent=closing', action: 'silent', editable: true, source: 'builtin',
  })
 })
 it('reads the effective rules view including disabled builtin switches', () => {
  const effective = toRulesEffective({
    BuiltinRules: [
      { RuleID: 'closing', Order: 10, Title: '告别结束', Condition: 'intent=closing', Action: 'silent', Editable: true, Source: 'builtin', Enabled: false, Applicable: true, Status: 'not_applicable', Reason: 'disabled' },
    ],
    FixedConstraints: [],
    ActiveRuleset: { ...raw, Status: 'published' },
    Version: 'builtin-v1',
    BuiltinRulesVersion: 'builtin-v1',
    Source: 'builtin',
  })
  expect(effective.builtinRules.find((rule) => rule.ruleId === 'closing')?.enabled).toBe(false)
  expect(effective.activeRuleset?.status).toBe('published')
  expect(effective.version).toBe('builtin-v1')
 })
 it('reads the actual nested server shape and round trips every edited field',()=>{
  const set=toRuleset(raw)
  expect(set.entries[0].action.reasonCode).toBe('quiet')
  expect(toRuleEntriesRequest(set.entries)).toEqual([{enabled:true,priority:5,match:{text_contains:'hello',min_seconds_since_reply:60},action:{type:'silent',reason_code:'quiet'}}])
 })
 it('preserves disabled entries, builtin switches and mixed conversation targets',()=>{
  const set=toRuleset({...raw,BuiltinRules:{closing:false},Targets:[{kind:'private',id:10},{kind:'group',id:20}],Entries:[{...raw.Entries[0],Enabled:false}]})
  expect(set.builtinRules).toEqual({closing:false})
  expect(set.targets).toEqual([{kind:'private',id:10},{kind:'group',id:20}])
  expect(toRuleEntriesRequest(set.entries)[0].enabled).toBe(false)
 })
 it('shows the final semantic reason and category instead of the preliminary rule reason',()=>{
  const simulation=toRuleSimulation({Blocked:false,Explicit:false,NeedSemantic:true,AlwaysReply:false,ForceReply:false,ReasonCode:'',Final:{Decision:'silent',ReasonCode:'closing',Provider:'openai',Model:'decision',LatencyMS:20,Scores:{directed_to_bot:0.4}},Steps:[{Phase:'semantic',Detail:{available:true,intent_category:'closing'}}]})
  expect(simulation.reasonCode).toBe('closing')
  expect(simulation.semanticCategory).toBe('closing')
  expect(simulation.semanticAvailable).toBe(true)
 })
 it('rejects invalid action values instead of showing an empty editor',()=>{
  expect(()=>toRuleset({...raw,Entries:[{...raw.Entries[0],Action:{type:'invalid'}}]})).toThrow()
 })
})
