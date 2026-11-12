// Fixed execution flow definitions for intent decisions.
// 意图决策的固定执行流程定义。
package domain

// Fixed execution flow version and stable node identifiers. These strings are
// part of the administrative API contract; changing one requires a new flow
// version and a deliberate front-end update.
// 固定执行流程版本与稳定节点标识。这些字符串属于管理 API 契约；
// 修改任何一个都必须发布新的流程版本并明确更新前端。
const (
	FlowVersion = "intent-flow-v1"

	NodePolicy   = "policy"
	NodeContext  = "context"
	NodeRules    = "rules"
	NodeSemantic = "semantic"
	NodeDecision = "decision"
)

// Fixed node and edge statuses exposed to the console.
// 暴露给控制台的固定节点与边状态。
const (
	NodeStatusExecuted = "executed"
	NodeStatusSkipped  = "skipped"
	NodeStatusFailed   = "failed"
	NodeStatusDegraded = "degraded"
	NodeStatusUnknown  = "unknown"

	EdgeStatusTaken    = "taken"
	EdgeStatusNotTaken = "not_taken"
	EdgeStatusUnknown  = "unknown"
)

// FlowNodeDefinition is one read-only node in the fixed flow.
// FlowNodeDefinition 是固定流程中的一个只读节点。
type FlowNodeDefinition struct {
	NodeID      string
	Kind        string
	Title       string
	Description string
	StageIndex  int
	Version     int
}

// FlowEdgeDefinition is one fixed edge in the flow.
// FlowEdgeDefinition 是流程中的一个固定边。
type FlowEdgeDefinition struct {
	EdgeID     string
	FromNodeID string
	ToNodeID   string
	Label      string
	Condition  string
}

// FlowDefinition is the complete fixed flow returned by the API.
// FlowDefinition 是 API 返回的完整固定流程。
type FlowDefinition struct {
	FlowVersion string
	Nodes       []FlowNodeDefinition
	Edges       []FlowEdgeDefinition
}

// FixedFlow returns the immutable P0 execution flow.
// FixedFlow 返回不可变的 P0 执行流程。
func FixedFlow() FlowDefinition {
	return FlowDefinition{
		FlowVersion: FlowVersion,
		Nodes: []FlowNodeDefinition{
			{NodeID: NodePolicy, Kind: "policy", Title: "策略与权限", Description: "读取生效作用域策略并检查权限、观察模式与频率闸门", StageIndex: 0, Version: 1},
			{NodeID: NodeContext, Kind: "context", Title: "上下文", Description: "按授权读取历史与记忆上下文", StageIndex: 1, Version: 1},
			{NodeID: NodeRules, Kind: "rules", Title: "规则", Description: "执行内置规则和版本化规则", StageIndex: 2, Version: 1},
			{NodeID: NodeSemantic, Kind: "semantic", Title: "语义判定", Description: "按需调用当前 Prompt/模型，记录跳过、失败或降级", StageIndex: 3, Version: 1},
			{NodeID: NodeDecision, Kind: "decision", Title: "最终决策", Description: "按规则与语义结果计算回复/沉默/延后", StageIndex: 4, Version: 1},
		},
		Edges: []FlowEdgeDefinition{
			{EdgeID: "e_policy_context", FromNodeID: NodePolicy, ToNodeID: NodeContext, Label: "继续", Condition: "策略可用"},
			{EdgeID: "e_context_rules", FromNodeID: NodeContext, ToNodeID: NodeRules, Label: "继续", Condition: "上下文处理完成"},
			{EdgeID: "e_rules_semantic", FromNodeID: NodeRules, ToNodeID: NodeSemantic, Label: "需要语义", Condition: "NeedSemantic && !Blocked && !Explicit && !AlwaysReply"},
			{EdgeID: "e_rules_decision", FromNodeID: NodeRules, ToNodeID: NodeDecision, Label: "规则直接结束", Condition: "!NeedSemantic || Blocked || Explicit || AlwaysReply"},
			{EdgeID: "e_semantic_decision", FromNodeID: NodeSemantic, ToNodeID: NodeDecision, Label: "语义完成", Condition: "SemanticCalled"},
		},
	}
}
