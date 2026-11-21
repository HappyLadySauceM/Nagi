package v1

// AccountView is one entry of the account list.
// AccountView 是账号列表中的一项。
type AccountView struct {
	BotID           int64  `json:"bot_id,omitempty"`
	Nickname        string `json:"nickname"`
	Endpoint        string `json:"endpoint"`
	ExpectedQQ      int64  `json:"expected_qq,omitempty"`
	Connected       bool   `json:"connected"`
	SelfIDKnown     bool   `json:"self_id_known"`
	Rejected        string `json:"rejected,omitempty"`
	HeartbeatSeen   bool   `json:"heartbeat_seen"`
	LastHeartbeatAt string `json:"last_heartbeat,omitempty"`
	SyncedAt        string `json:"synced_at,omitempty"`
}

// IngressView describes the process-local message gate and active dispatch count.
// IngressView 描述本进程的消息闸门及正在分发的消息数。
type IngressView struct {
	Paused      bool `json:"paused"`
	Dispatching int  `json:"dispatching"`
}
