// Run projection shared by the decision detail and simulation endpoints.
// 决策详情与模拟接口共享的运行投影。
package application

import (
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// BuildRunFromRecord converts a persisted decision into the fixed-flow run contract.
// BuildRunFromRecord 将持久化决策转换为固定流程运行契约。
func BuildRunFromRecord(record contracts.DecisionRecord, delivery contracts.Delivery) contracts.Run {
	status := record.SnapshotStatus
	if status == "" {
		status = domain.SnapshotStatusLegacy
	}
	missing := append([]string(nil), record.MissingFields...)
	if status == domain.SnapshotStatusLegacy && len(missing) == 0 {
		missing = []string{"snapshot"}
	}
	run := contracts.Run{
		DecisionID: record.ID, JobID: record.JobID, RunID: record.RunID,
		FlowVersion: firstFlowVersion(record.FlowVersion), Provider: record.Provider, Model: record.Model,
		RulesetVersion: record.RulesetVersion, SnapshotStatus: status, MissingFields: missing,
		Summary:  contracts.RunSummary{Decision: record.Decision, ReasonCode: record.ReasonCode, ReasonText: domain.ReasonText(record.ReasonCode)},
		Delivery: delivery, Scores: record.Scores, LatencyMS: int64(record.LatencyMS), CreatedAt: record.CreatedAt,
	}
	for _, step := range record.Steps {
		node := contracts.RunNode{
			NodeID: step.NodeID, Version: step.NodeVersion, Status: step.Status, Branch: step.Branch, ReasonCode: step.ReasonCode,
			Summary: step.Summary, Detail: step.Detail, Evidence: step.Evidence,
			DurationMS: step.DurationMS,
		}
		if node.Status == "" {
			node.Status = domain.NodeStatusUnknown
		}
		if node.NodeID == "" {
			node.NodeID = step.Phase
		}
		run.Nodes = append(run.Nodes, node)
	}
	run.Edges = deriveEdges(run.Nodes)
	return run
}

// BuildRunFromDecision converts a live/simulated decision into the same run contract.
// BuildRunFromDecision 将实时或模拟决策转换为同一运行契约。
func BuildRunFromDecision(final domain.FinalDecision, delivery contracts.Delivery) contracts.Run {
	run := contracts.Run{
		DecisionID: 0, JobID: 0, RunID: final.RunID,
		FlowVersion: firstFlowVersion(final.FlowVersion), Provider: final.Provider, Model: final.Model,
		RulesetVersion: final.RulesetVersion, SnapshotStatus: domain.SnapshotStatusMissing,
		MissingFields: []string{"not_persisted"},
		Summary:       contracts.RunSummary{Decision: string(final.Decision), ReasonCode: final.ReasonCode, ReasonText: domain.ReasonText(final.ReasonCode)},
		Delivery:      delivery, Scores: final.Scores, LatencyMS: final.LatencyMS, CreatedAt: time.Now(),
	}
	for _, step := range final.Steps {
		run.Nodes = append(run.Nodes, contracts.RunNode{
			NodeID: step.NodeID, Version: step.NodeVersion, Status: firstStatus(step.Status), Branch: step.Branch, ReasonCode: step.ReasonCode,
			Summary: step.Summary, Detail: step.Detail, Evidence: evidenceToContract(step.Evidence), DurationMS: step.DurationMS,
		})
	}
	run.Edges = deriveEdges(run.Nodes)
	return run
}

func firstFlowVersion(version string) string {
	if version == "" {
		return domain.FlowVersion
	}
	return version
}

func firstStatus(status string) string {
	if status == "" {
		return domain.NodeStatusUnknown
	}
	return status
}

func evidenceToContract(items []domain.Evidence) []contracts.EvidenceItem {
	out := make([]contracts.EvidenceItem, 0, len(items))
	for _, item := range items {
		out = append(out, contracts.EvidenceItem{Kind: item.Kind, Text: item.Text})
	}
	return out
}

// deriveEdges computes edge statuses from the fixed flow and actual node results.
// It never invents a branch that the node results cannot support.
// deriveEdges 用固定流程与实际节点结果计算边状态，不凭空创造节点结果无法支撑的分支。
func deriveEdges(nodes []contracts.RunNode) []contracts.RunEdge {
	status := make(map[string]string, len(nodes))
	for _, node := range nodes {
		status[node.NodeID] = node.Status
	}
	edges := domain.FixedFlow().Edges
	out := make([]contracts.RunEdge, 0, len(edges))
	for _, edge := range edges {
		edgeStatus := domain.EdgeStatusUnknown
		switch edge.EdgeID {
		case "e_policy_context":
			edgeStatus = edgeIf(status[domain.NodePolicy] != "", domain.EdgeStatusTaken)
		case "e_context_rules":
			edgeStatus = edgeIf(status[domain.NodeContext] != "", domain.EdgeStatusTaken)
		case "e_rules_semantic":
			if s, ok := status[domain.NodeSemantic]; ok {
				if s == domain.NodeStatusSkipped {
					edgeStatus = domain.EdgeStatusNotTaken
				} else {
					edgeStatus = domain.EdgeStatusTaken
				}
			}
		case "e_rules_decision":
			if s, ok := status[domain.NodeSemantic]; ok {
				if s == domain.NodeStatusSkipped {
					edgeStatus = domain.EdgeStatusTaken
				} else {
					edgeStatus = domain.EdgeStatusNotTaken
				}
			}
		case "e_semantic_decision":
			if s, ok := status[domain.NodeSemantic]; ok && s != domain.NodeStatusSkipped {
				edgeStatus = domain.EdgeStatusTaken
			} else if ok {
				edgeStatus = domain.EdgeStatusNotTaken
			}
		}
		out = append(out, contracts.RunEdge{EdgeID: edge.EdgeID, Status: edgeStatus})
	}
	return out
}

func edgeIf(condition bool, value string) string {
	if condition {
		return value
	}
	return domain.EdgeStatusUnknown
}

// BuildRuleRun projects a rules-only simulation into the same run contract.
// BuildRuleRun 将只运行规则的模拟投影为同一运行契约。
func BuildRuleRun(in domain.Input, rule domain.RuleResult, th domain.Thresholds, flowVersion string) contracts.Run {
	decision := domain.FinalDecision{Decision: domain.DecisionDefer, ReasonCode: rule.ReasonCode, FlowVersion: flowVersion}
	if rule.Blocked {
		decision.Decision = domain.DecisionSilent
	} else if rule.ForceReply {
		decision.Decision = domain.DecisionReply
		decision.ReasonCode = rule.ForceReason
	} else if rule.AlwaysReply {
		decision.Decision = domain.DecisionReply
		decision.ReasonCode = "always_reply"
	} else if rule.Explicit {
		decision.Decision = domain.DecisionReply
		decision.ReasonCode = "explicit_mention"
	}
	decision.Steps = decisionSteps(in, rule, domain.SemanticAnswer{}, decision, 0, false, th, false)
	return BuildRunFromDecision(decision, contracts.Delivery{})
}
