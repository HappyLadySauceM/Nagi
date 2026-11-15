package domain

// PromptSnapshot is the immutable prompt metadata persisted with one memory
// extraction task. It intentionally contains no message or prompt body.
// PromptSnapshot 是随单个记忆抽取任务持久化的不可变 Prompt 元数据；
// 有意不保存消息正文或 Prompt 正文。
type PromptSnapshot struct {
	PromptID       string                `json:"PromptID"`
	Kind           string                `json:"Kind"`
	Provider       string                `json:"Provider"`
	BotID          int64                 `json:"BotID"`
	TargetType     string                `json:"TargetType"`
	TargetID       int64                 `json:"TargetID"`
	ConversationID string                `json:"ConversationID"`
	FinalHash      string                `json:"FinalHash"`
	RenderedHash   string                `json:"RenderedHash"`
	Layers         []PromptSnapshotLayer `json:"Layers"`
}

// PromptSnapshotLayer identifies one immutable prompt layer used by a task.
// PromptSnapshotLayer 标识任务使用的单个不可变 Prompt 层。
type PromptSnapshotLayer struct {
	Layer     string `json:"Layer"`
	VersionID int64  `json:"VersionID"`
	Version   int    `json:"Version"`
	Source    string `json:"Source"`
	Hash      string `json:"Hash"`
}

// ExtractionPrompt is the resolved in-memory prompt and its persisted snapshot.
// ExtractionPrompt 是解析后的内存 Prompt 及其待持久化快照。
type ExtractionPrompt struct {
	Snapshot     PromptSnapshot
	SystemPrompt string
	UserTemplate string
}
