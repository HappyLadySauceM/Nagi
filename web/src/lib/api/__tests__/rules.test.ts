import { describe, expect, it } from 'vitest'
import { toRuleEntriesRequest, toRuleset, toRuleSimulation } from '../adapters/rules'

const raw = { ID:1, BotID:2, ScopeType:'private', ScopeID:3, Version:1, Status:'draft', Note:'', Entries:[{ Priority:5, Match:{text_contains:'hello',min_seconds_since_reply:60},Action:{type:'silent',reason_code:'quiet'}}] }
describe('rule wire boundary',()=>{
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
