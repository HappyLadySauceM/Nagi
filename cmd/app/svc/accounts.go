package svc

import (
	"fmt"
	"sync"
	"time"

	agentapp "github.com/HappyLadySauceM/QQBot/internal/agent/application"
	obapp "github.com/HappyLadySauceM/QQBot/internal/onebot/application"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
)

// AccountConnection is one configured SnowLuma endpoint together with the
// OneBot client bound to it. The client owns the echo table and event stream,
// so two connections never share action responses or events.
// AccountConnection 是一条已配置的 SnowLuma 端点及其绑定的 OneBot 客户端。
// 客户端独占 echo 匹配表与事件流，两条连接不会共享响应或事件。
type AccountConnection struct {
	Endpoint   string
	ExpectedQQ int64
	Client     *obapp.ClientService

	mu       sync.RWMutex
	rejected string
}

// NewAccountConnection binds an endpoint to its client.
// NewAccountConnection 将端点与客户端绑定。
func NewAccountConnection(endpoint string, expectedQQ int64, client *obapp.ClientService) *AccountConnection {
	return &AccountConnection{Endpoint: endpoint, ExpectedQQ: expectedQQ, Client: client}
}

// Reject marks the connection unusable and records why, for example when the
// discovered account does not match expect-qq.
// Reject 标记连接不可用并记录原因，例如识别到的账号与 expect-qq 不一致。
func (c *AccountConnection) Reject(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rejected = reason
}

// Rejected returns the rejection reason, or an empty string when usable.
// Rejected 返回拒绝原因；可用时返回空字符串。
func (c *AccountConnection) Rejected() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rejected
}

// BotID returns the identity discovered from get_login_info or the first event.
// BotID 返回由 get_login_info 或首个事件识别出的账号身份。
func (c *AccountConnection) BotID() (int64, bool) {
	id, ok := c.Client.SelfID()
	return int64(id), ok
}

// Connected reports the transport state of this endpoint only.
// Connected 仅报告本端点的传输状态。
func (c *AccountConnection) Connected() bool { return c.Client.Connected() }

// Status returns this connection's snapshot.
// Status 返回本连接的快照。
func (c *AccountConnection) Status() obcontracts.BotStatus { return c.Client.Status() }

// AccountStatus is the read-only view served by the management API.
// AccountStatus 是管理 API 提供的只读账号视图。
type AccountStatus struct {
	BotID           int64
	Endpoint        string
	ExpectedQQ      int64
	Connected       bool
	SelfIDKnown     bool
	Rejected        string
	HeartbeatSeen   bool
	LastHeartbeatAt time.Time
}

// Accounts is the account registry: it routes sends and queries by bot id and
// defines the single-account default rule.
// Accounts 是账号注册表：按 bot id 路由发送与查询，并定义单账号默认规则。
type Accounts struct {
	mu   sync.RWMutex
	list []*AccountConnection
}

// NewAccounts builds an empty registry.
// NewAccounts 构建空的注册表。
func NewAccounts() *Accounts { return &Accounts{} }

// Add registers one connection; duplicate endpoints are rejected.
// Add 注册一条连接；重复端点会被拒绝。
func (a *Accounts) Add(conn *AccountConnection) error {
	if conn == nil || conn.Client == nil || conn.Endpoint == "" {
		return fmt.Errorf("svc: account connection required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, existing := range a.list {
		if existing.Endpoint == conn.Endpoint {
			return fmt.Errorf("svc: endpoint %s already registered", conn.Endpoint)
		}
	}
	a.list = append(a.list, conn)
	return nil
}

// List snapshots the configured connections in configuration order.
// List 按配置顺序返回连接的快照。
func (a *Accounts) List() []*AccountConnection {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*AccountConnection, len(a.list))
	copy(out, a.list)
	return out
}

// Lookup resolves a connected, non-rejected connection by bot id.
// Lookup 按 bot id 解析已连接且未被拒绝的连接。
func (a *Accounts) Lookup(botID int64) (*AccountConnection, bool) {
	if botID <= 0 {
		return nil, false
	}
	for _, conn := range a.List() {
		if conn.Rejected() != "" {
			continue
		}
		if id, ok := conn.BotID(); ok && id == botID {
			return conn, true
		}
	}
	return nil, false
}

// Default returns the only configured connection. With several accounts the
// caller must name one explicitly, so no implicit route can pick the wrong bot.
// Default 返回唯一配置的连接。多账号时调用方必须显式指定，避免隐式选中错误账号。
func (a *Accounts) Default() (*AccountConnection, bool) {
	list := a.List()
	if len(list) != 1 {
		return nil, false
	}
	return list[0], true
}

// InfoReader implements directory's account-scoped reader port.
// InfoReader 实现 directory 的按账号解析查询能力端口。
func (a *Accounts) InfoReader(botID int64) (obcontracts.InfoReader, bool) {
	conn, ok := a.Lookup(botID)
	if !ok {
		return nil, false
	}
	return conn.Client, true
}

// Sender implements echo's account-scoped sending port.
// Sender 实现 echo 的按账号解析发送能力端口。
func (a *Accounts) Sender(botID int64) (obcontracts.Sender, bool) {
	conn, ok := a.Lookup(botID)
	if !ok {
		return nil, false
	}
	return conn.Client, true
}

// ReceiptSender implements the agent's account-scoped sender port.
// ReceiptSender 实现 agent 的按账号解析发送能力端口。
func (a *Accounts) ReceiptSender(botID int64) (agentapp.ReceiptSender, bool) {
	conn, ok := a.Lookup(botID)
	if !ok {
		return nil, false
	}
	return conn.Client, true
}

// ConnectedAny reports whether at least one account is connected.
// ConnectedAny 报告是否至少有一个账号已连接。
func (a *Accounts) ConnectedAny() bool {
	for _, conn := range a.List() {
		if conn.Rejected() == "" && conn.Connected() {
			return true
		}
	}
	return false
}

// Snapshot builds the API view of every configured account.
// Snapshot 构建全部已配置账号的 API 视图。
func (a *Accounts) Snapshot() []AccountStatus {
	list := a.List()
	out := make([]AccountStatus, 0, len(list))
	for _, conn := range list {
		status := conn.Status()
		item := AccountStatus{
			BotID:           int64(status.SelfID),
			Endpoint:        conn.Endpoint,
			ExpectedQQ:      conn.ExpectedQQ,
			Connected:       status.Connected,
			SelfIDKnown:     status.SelfIDKnown,
			Rejected:        conn.Rejected(),
			HeartbeatSeen:   status.HeartbeatSeen,
			LastHeartbeatAt: status.LastHeartbeatAt,
		}
		out = append(out, item)
	}
	return out
}
