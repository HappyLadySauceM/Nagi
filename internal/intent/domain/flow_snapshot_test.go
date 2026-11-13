// Golden snapshot for the fixed intent flow contract.
// 固定意图流程契约的漂移快照。
package domain

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateFlowSnapshot = flag.Bool("update", false, "更新固定流程契约快照")

type fixedFlowSnapshot struct {
	FlowVersion     string
	SnapshotVersion string
	Nodes           []FlowNodeDefinition
	Edges           []FlowEdgeDefinition
}

// TestFixedFlowContractSnapshot pins the fixed flow version, snapshot version
// and every node/edge definition. These values are part of the admin API
// contract; changing them requires a deliberate product decision and a
// front-end update.
// TestFixedFlowContractSnapshot 固定流程版本、快照版本及全部节点/边定义。
// 这些值属于管理 API 契约，变更需产品确认并同步前端。
func TestFixedFlowContractSnapshot(t *testing.T) {
	flow := FixedFlow()
	got, err := json.MarshalIndent(fixedFlowSnapshot{
		FlowVersion:     flow.FlowVersion,
		SnapshotVersion: SnapshotVersion,
		Nodes:           flow.Nodes,
		Edges:           flow.Edges,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal flow snapshot: %v", err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "flow-contract.golden.json")
	if *updateFlowSnapshot {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create snapshot dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write flow snapshot: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取固定流程快照失败: %v\n首次生成或有意更新请运行：go test ./internal/intent/domain -run TestFixedFlowContractSnapshot -update", err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("固定流程契约发生漂移：\nFlowVersion/SnapshotVersion/节点/边属于管理 API 契约，必须由产品确认并同步前端。\n若为有意变更，请更新快照并在 PR 写明产品确认依据：\n  go test ./internal/intent/domain -run TestFixedFlowContractSnapshot -update\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}
