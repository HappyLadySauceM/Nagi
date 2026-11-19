package v1

// PolicyUpdate is the accepted JSON body of PUT /api/v1/scopes/{type}/{id}.
// PolicyUpdate 是 PUT /api/v1/scopes/{type}/{id} 接受的 JSON 请求体。
type PolicyUpdate struct {
	MaxMessagesPerRun       int    `json:"max_messages_per_run"`
	MaxCharsPerMessage      int    `json:"max_chars_per_message"`
	MaxTotalCharsPerRun     int    `json:"max_total_chars_per_run"`
	MessageIntervalMS       *int   `json:"message_interval_ms"`
	ExpectedTemplateVersion *int64 `json:"expected_template_version"`
	Enabled                 bool   `json:"enabled"`
	ReplyMode               string `json:"reply_mode"`
	// StoreMessages is optional: storage is a product baseline, so an absent
	// field keeps it on while an explicit false still works for scripts.
	// StoreMessages 可选：消息存储是产品基线，字段缺省时保持开启；
	// 显式 false 仍可用于脚本。
	StoreMessages      *bool           `json:"store_messages"`
	AllowMemoryWrite   bool            `json:"allow_memory_write"`
	AllowMemoryRead    bool            `json:"allow_memory_read"`
	AllowHistoryRead   bool            `json:"allow_history_read"`
	MinReplyIntervalMS int             `json:"min_reply_interval_ms"`
	MaxRepliesPerHour  int             `json:"max_replies_per_hour"`
	ToolPermissions    map[string]bool `json:"tool_permissions"`
}
