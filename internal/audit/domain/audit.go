// Package domain defines the admin audit log entry model.
// audit/domain 定义管理审计日志条目模型。
package domain

// Entry is one admin operation record; Before/After carry JSON snapshots when available.
// Entry 是一条管理操作记录；可用时 Before/After 携带 JSON 快照。
type Entry struct {
	Actor    string
	Action   string
	Resource string
	Before   []byte
	After    []byte
}
