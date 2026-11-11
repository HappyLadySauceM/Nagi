package infrastructure

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
)

// memoryAuthorization is the generation stamped onto messages at write time.
// memoryAuthorization 是消息写入时记录的授权代际。
type memoryAuthorization struct {
	EventID    int64
	Authorized bool
}

// AuthorizationReader reads authorization state from a SQL transaction.
// AuthorizationReader 从 SQL 事务中读取授权状态。
type AuthorizationReader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// AuthorizationExecutor runs authorization lock queries on a SQL transaction.
// AuthorizationExecutor 在 SQL 事务中执行授权锁查询。
type AuthorizationExecutor interface {
	AuthorizationReader
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type policyAuthorization struct {
	Enabled    bool
	Store      bool
	AllowWrite bool
	EventID    sql.NullInt64
}

// ResolveMemoryAuthorization returns the current generation and whether the
// effective policy still authorizes memory extraction.
// ResolveMemoryAuthorization 返回当前授权代际，以及有效策略是否仍允许记忆提取。
func (r *Repo) ResolveMemoryAuthorization(ctx context.Context, botID int64, scopeType domain.ScopeType, scopeID int64) (int64, bool, error) {
	if botID <= 0 || scopeID < 0 {
		return 0, false, fmt.Errorf("scope: invalid memory authorization identity")
	}
	auth, err := resolveMemoryAuthorization(ctx, r.pool.Client, botID, string(scopeType), scopeID)
	if err != nil {
		return 0, false, err
	}
	return auth.EventID, auth.Authorized, nil
}

// ResolveMemoryAuthorizationInTx reads authorization state through the caller's
// transaction, so callers can keep policy resolution and their own update atomic.
// ResolveMemoryAuthorizationInTx 通过调用方事务读取授权状态，使策略解析与自身写入保持原子性。
func ResolveMemoryAuthorizationInTx(ctx context.Context, q AuthorizationReader, botID int64, scopeType string, scopeID int64) (int64, bool, error) {
	auth, err := resolveMemoryAuthorization(ctx, q, botID, scopeType, scopeID)
	if err != nil {
		return 0, false, err
	}
	return auth.EventID, auth.Authorized, nil
}

// StampMemoryAuthorization locks the authorization keys in the caller's message
// transaction and re-reads the effective policy before returning its generation.
// StampMemoryAuthorization 在调用方消息事务内锁定授权键并重新读取有效策略，
// 返回该策略的授权代际，避免在事务外读取后盖上过期事件。
func (r *Repo) StampMemoryAuthorization(ctx context.Context, tx *ent.Tx, botID int64, scopeType string, scopeID int64) (int64, bool, error) {
	if tx == nil {
		return 0, false, fmt.Errorf("scope: transaction required")
	}
	client := tx.Client()
	if err := lockMemoryAuthorization(ctx, client, botID, scopeType, scopeID); err != nil {
		return 0, false, err
	}
	auth, err := resolveMemoryAuthorization(ctx, client, botID, scopeType, scopeID)
	if err != nil {
		return 0, false, err
	}
	return auth.EventID, auth.Authorized, nil
}

// lockMemoryAuthorization serializes policy writes and message/finalize reads
// with the same direct-then-template lock order.
// lockMemoryAuthorization 按“独立作用域→模板”的固定顺序串行化策略写入与
// 消息/最终提交读取。
func lockMemoryAuthorization(ctx context.Context, client *ent.Client, botID int64, scopeType string, scopeID int64) error {
	if client == nil {
		return fmt.Errorf("scope: client required")
	}
	return LockMemoryAuthorization(ctx, client, botID, scopeType, scopeID)
}

// LockMemoryAuthorization serializes policy writes with message/finalize reads
// using the same direct-then-template advisory lock order.
// LockMemoryAuthorization 按“独立作用域→模板”的固定顺序串行化策略写入与
// 消息/最终提交读取。
func LockMemoryAuthorization(ctx context.Context, executor AuthorizationExecutor, botID int64, scopeType string, scopeID int64) error {
	if executor == nil {
		return fmt.Errorf("scope: authorization executor required")
	}
	keys := []string{
		fmt.Sprintf("memory-auth:%d:%s:%d", botID, scopeType, scopeID),
		fmt.Sprintf("memory-auth:%d:%s:0", botID, scopeType),
	}
	for _, key := range keys {
		if _, err := executor.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", key); err != nil {
			return fmt.Errorf("scope: lock memory authorization: %w", err)
		}
	}
	return nil
}

func resolveMemoryAuthorization(ctx context.Context, q AuthorizationReader, botID int64, scopeType string, scopeID int64) (memoryAuthorization, error) {
	direct, directFound, err := loadPolicyAuthorization(ctx, q, botID, scopeType, scopeID)
	if err != nil {
		return memoryAuthorization{}, err
	}
	template, templateFound, err := loadPolicyAuthorization(ctx, q, botID, scopeType, 0)
	if err != nil {
		return memoryAuthorization{}, err
	}

	effective := policyAuthorization{Store: true}
	if directFound {
		effective = direct
	} else if templateFound {
		effective = template
	}

	eventID := int64(0)
	if directFound {
		eventID = direct.EventID.Int64
		if eventID == 0 {
			eventID, err = latestAuthorizationEventID(ctx, q, botID, scopeType, scopeID, "")
			if err != nil {
				return memoryAuthorization{}, err
			}
		}
	} else {
		directRemoved, err := latestAuthorizationEventID(ctx, q, botID, scopeType, scopeID, "removed")
		if err != nil {
			return memoryAuthorization{}, err
		}
		if directRemoved > eventID {
			eventID = directRemoved
		}
		if templateFound {
			if template.EventID.Valid && template.EventID.Int64 > eventID {
				eventID = template.EventID.Int64
			} else if !template.EventID.Valid || template.EventID.Int64 == 0 {
				templateEvent, err := latestAuthorizationEventID(ctx, q, botID, scopeType, 0, "")
				if err != nil {
					return memoryAuthorization{}, err
				}
				if templateEvent > eventID {
					eventID = templateEvent
				}
			}
		} else {
			templateRemoved, err := latestAuthorizationEventID(ctx, q, botID, scopeType, 0, "removed")
			if err != nil {
				return memoryAuthorization{}, err
			}
			if templateRemoved > eventID {
				eventID = templateRemoved
			}
		}
	}

	return memoryAuthorization{
		EventID:    eventID,
		Authorized: effective.Enabled && effective.Store && effective.AllowWrite,
	}, nil
}

func loadPolicyAuthorization(ctx context.Context, q AuthorizationReader, botID int64, scopeType string, scopeID int64) (policyAuthorization, bool, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT enabled, store_messages, allow_memory_write, memory_authorization_event_id
		FROM scope_policies
		WHERE bot_id=$1 AND scope_type=$2 AND scope_id=$3`, botID, scopeType, scopeID)
	if err != nil {
		return policyAuthorization{}, false, fmt.Errorf("scope: read memory authorization policy: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return policyAuthorization{}, false, err
		}
		return policyAuthorization{}, false, nil
	}
	var out policyAuthorization
	if err := rows.Scan(&out.Enabled, &out.Store, &out.AllowWrite, &out.EventID); err != nil {
		return policyAuthorization{}, false, fmt.Errorf("scope: scan memory authorization policy: %w", err)
	}
	return out, true, nil
}

func latestAuthorizationEventID(ctx context.Context, q AuthorizationReader, botID int64, scopeType string, scopeID int64, kind string) (int64, error) {
	query := `SELECT id FROM scope_memory_authorization_events WHERE bot_id=$1 AND scope_type=$2 AND scope_id=$3`
	args := []any{botID, scopeType, scopeID}
	if kind != "" {
		query += ` AND event_kind=$4`
		args = append(args, kind)
	}
	query += ` ORDER BY id DESC LIMIT 1`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("scope: read memory authorization event: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		return 0, fmt.Errorf("scope: scan memory authorization event: %w", err)
	}
	return id, nil
}

// insertMemoryAuthorizationEvent records one immutable transition and returns
// its generation id. It must run inside the policy write transaction.
// insertMemoryAuthorizationEvent 记录一次不可变转换并返回代际 ID；必须在策略
// 写入事务内执行。
func insertMemoryAuthorizationEvent(ctx context.Context, client *ent.Client, botID int64, scopeType string, scopeID int64, kind string, authorized bool, policyVersion int64) (int64, error) {
	rows, err := client.QueryContext(ctx, `
		INSERT INTO scope_memory_authorization_events
			(bot_id, scope_type, scope_id, event_kind, authorized, policy_version, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp())
		RETURNING id`, botID, scopeType, scopeID, kind, authorized, policyVersion)
	if err != nil {
		return 0, fmt.Errorf("scope: insert memory authorization event: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, fmt.Errorf("scope: insert memory authorization event returned no id")
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		return 0, fmt.Errorf("scope: scan memory authorization event id: %w", err)
	}
	return id, nil
}
