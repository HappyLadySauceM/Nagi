// Package contracts publishes the read-only long-term memory port.
// memory/contracts 发布只读长期记忆端口。
package contracts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	memorydomain "github.com/HappyLadySauceM/Nagi/internal/memory/domain"
)

// Memory is one approved memory entry handed to another business context.
// Memory 是交给其他业务上下文的单条已批准记忆。
type Memory struct {
	AuthorizationVersion int64
	ConsentIdentity      string
	ID                   string
	Content              string
	Visibility           string
	Source               string
	SourceMessageID      int64
	SourceSenderID       int64
	Origin               string
	SubjectType          string
	SubjectID            string
	ReviewReason         string
	UpdatedAt            time.Time
}

// RecallRequest is the visibility-scoped read request of one reply run.
// RecallRequest 是单次回复运行中按可见性过滤的读取请求。
type RecallRequest struct {
	BotID          int64
	Kind           string
	TargetID       int64
	ConversationID string
	Query          string
	AllowRewrite   bool
	Limit          int
	// SpeakerType/SpeakerID identify the current message sender. They are
	// separate from the subject being asked about and never imply the subject.
	// SpeakerType/SpeakerID 标识当前消息发送者，与查询主体分离，不能据此推断主体。
	SpeakerType string
	SpeakerID   string
	// SubjectType/SubjectID are the target subject of this recall.
	// SubjectType/SubjectID 是本次召回的目标主体。
	SubjectType string
	SubjectID   string
	// SubjectSource is the deterministic source that supplied SubjectID.
	// SubjectSource 是对 SubjectID 的确定性来源。
	SubjectSource string
	// SubjectEvidence is a masked source identifier such as mention:1001.
	// SubjectEvidence 是脱敏来源标识，例如 mention:1001。
	SubjectEvidence string
	// SubjectAmbiguous marks an unresolved subject ambiguity.
	// SubjectAmbiguous 标记主体无法消歧。
	SubjectAmbiguous bool
}

// RecallItem is one ranked memory with its audit fields.
// RecallItem 是一条带排序与命中审计信息的记忆。
type RecallItem struct {
	AuthorizationVersion int64  `json:"-"`
	ConsentIdentity      string `json:"-"`
	MemoryID             string
	Content              string
	Visibility           string
	SubjectType          string
	SubjectID            string
	Source               string
	SourceMessageID      int64
	SourceSenderID       int64
	Score                float64
	HitKeywords          []string
	SubjectMatch         string
	SelectionReason      string
	UpdatedAt            time.Time `json:"-"`
	// Memory carries the original domain row for compatibility views; it is
	// deliberately excluded from the recall audit JSON.
	// Memory 保留原始领域行供兼容视图使用，召回审计 JSON 不输出该字段。
	Memory memorydomain.Memory `json:"-"`
}

// RecallResult is the bounded result of one recall request.
// RecallResult 是单次召回请求的有界结果。
type RecallResult struct {
	Items     []RecallItem
	NoKeyword bool
	Source    string
	Limit     int
	// Reason distinguishes no_keyword from no_match for diagnostics.
	// Reason 用于区分 no_keyword 与 no_match，便于调试。
	Reason string
}

// Reader loads ranked, approved, unexpired memories visible to one conversation.
// Reader 读取某会话可见且经过排序的已批准、未过期记忆。
type Reader interface {
	Recall(ctx context.Context, req RecallRequest) (RecallResult, error)
}

// DetailRequest asks for one memory that is visible to one conversation.
// DetailRequest 请求一条对指定会话可见的记忆详情。
type DetailRequest struct {
	BotID          int64
	Kind           string
	TargetID       int64
	ConversationID string
	MemoryID       string
	SenderID       int64
}

// DetailReader reads one visible memory with its stored source metadata.
// DetailReader 读取一条可见记忆及其已存来源信息。
type DetailReader interface {
	RecallDetail(ctx context.Context, req DetailRequest) (Memory, bool, error)
}

// RunReads tracks memory versions and source grants already used by a reply run.
// RunReads 记录回复运行已使用的记忆版本与来源授权。
type RunReads struct {
	mu           sync.Mutex
	checks       []func(context.Context) error
	crossGroup   []ConsentRead
	local        []LocalRead
	dependencies []ReadDependency
}
type runReadsKey struct{}

type replyJobKey struct{}

// WithReplyJob carries worker identity. The repository independently checks the live claim and inbound row.
// WithReplyJob 传递工作器身份，仓储独立核验当前租约与已存入站。
func WithReplyJob(ctx context.Context, jobID int64, claim string) context.Context {
	return context.WithValue(ctx, replyJobKey{}, memorydomain.ReplyJob{ID: jobID, Claim: claim})
}

func ReplyJobFrom(ctx context.Context) memorydomain.ReplyJob {
	job, _ := ctx.Value(replyJobKey{}).(memorydomain.ReplyJob)
	return job
}

// ErrReadChanged ends a run whose model context can no longer be sent safely.
// ErrReadChanged 终止模型上下文已不可继续提交的运行。
var ErrReadChanged = errors.New("memory: reply context authorization or version changed")

// CrossGroupReadUsed reports whether this turn has incorporated a cross-group fact.
// CrossGroupReadUsed 表示本轮已采用跨群事实。
func CrossGroupReadUsed(ctx context.Context) bool {
	reads, _ := ctx.Value(runReadsKey{}).(*RunReads)
	if reads == nil {
		return false
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return len(reads.crossGroup) > 0
}

// ConsentRead binds a derived result to the live job and exact grant used.
// ConsentRead 将派生内容绑定到当前任务及实际采用的授权实例。
type ConsentRead struct {
	BotID, GroupID, JobID, FactVersion              int64
	Kind, ConversationID, Claim, MemoryID, Identity string
}

func ConsentReads(ctx context.Context) []ConsentRead {
	reads, _ := ctx.Value(runReadsKey{}).(*RunReads)
	if reads == nil {
		return nil
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return slices.Clone(reads.crossGroup)
}

// ConsentReadDigest binds a persisted delivery to its exact disclosure set without storing claims.
// ConsentReadDigest 将持久化投递绑定到读取记录，台账无需保存额外的租约凭据。
func ConsentReadDigest(reads []ConsentRead) string {
	stable := slices.Clone(reads)
	for i := range stable {
		stable[i].Claim = ""
	}
	slices.SortFunc(stable, func(a, b ConsentRead) int {
		if byFact := strings.Compare(a.MemoryID, b.MemoryID); byFact != 0 {
			return byFact
		}
		return strings.Compare(a.Identity, b.Identity)
	})
	encoded, _ := json.Marshal(stable)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type consentDestinationKey struct{}

// GenerationDispatch describes the fixed physical request at its first-write boundary.
// GenerationDispatch 描述首写边界处已固定的物理请求。
type GenerationDispatch struct {
	Endpoint, Model string
	Reads           []ConsentRead
	LocalReads      []LocalRead
}

type LocalRead struct {
	DetailRequest
	FactVersion, SourceMessageID, SourceSenderID int64
	UpdatedAt                                    time.Time
	HistorySource                                bool
}

// GenerationReads takes one snapshot of both classes of memory dependencies.
// GenerationReads 在同一个快照中取出两类记忆依赖。
func GenerationReads(ctx context.Context) ([]ConsentRead, []LocalRead) {
	reads, _ := ctx.Value(runReadsKey{}).(*RunReads)
	if reads == nil {
		return nil, nil
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return slices.Clone(reads.crossGroup), slices.Clone(reads.local)
}

// WithConsentDestination binds a run to its durable generation authority.
// WithConsentDestination 将运行绑定到持久化生成授权。
func WithConsentDestination(ctx context.Context, validate func(context.Context, GenerationDispatch) error) context.Context {
	return context.WithValue(ctx, consentDestinationKey{}, validate)
}

func ValidateConsentDestination(ctx context.Context, request GenerationDispatch) error {
	validate, _ := ctx.Value(consentDestinationKey{}).(func(context.Context, GenerationDispatch) error)
	if validate == nil || len(request.Reads) == 0 || validate(ctx, request) != nil {
		return ErrReadChanged
	}
	return nil
}

// WithRunReads starts a fresh ledger for each reply attempt.
// WithRunReads 为每次回复尝试建立独立的读取记录。
func WithRunReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, runReadsKey{}, &RunReads{})
}

// ReadDependency contains the identifiers needed to verify a previous disclosure.
// ReadDependency 仅包含重新核验既有披露所需的标识。
type ReadDependency struct {
	ID              string    `json:"id"`
	UpdatedAt       time.Time `json:"updated_at"`
	SourceMessageID int64     `json:"source_message_id,omitempty"`
}

// ReadDependencies returns a snapshot of the disclosed memory versions.
// ReadDependencies 返回本回合已披露的记忆版本快照。
func ReadDependencies(ctx context.Context) []ReadDependency {
	reads, _ := ctx.Value(runReadsKey{}).(*RunReads)
	if reads == nil {
		return nil
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return append([]ReadDependency(nil), reads.dependencies...)
}

// TrackRead records the full persisted version and the live authority for one disclosure.
// TrackRead 记录一次披露使用的完整持久化版本和实时授权。
func TrackRead(ctx context.Context, reader DetailReader, req DetailRequest, version time.Time, authorize func(context.Context) error) {
	TrackVersionedRead(ctx, reader, req, Memory{UpdatedAt: version}, authorize)
}

// TrackVersionedRead binds rereads to both the fact version and the exact authorization instance.
// TrackVersionedRead 将复核绑定到事实版本和本次授权实例。
func TrackVersionedRead(ctx context.Context, reader DetailReader, req DetailRequest, version Memory, authorize func(context.Context) error) {
	trackVersionedRead(ctx, reader, req, version, authorize, false)
}

// TrackHistorySourceRead also requires current history authorization at physical dispatch.
// TrackHistorySourceRead 同时要求物理派发时的历史读取授权有效。
func TrackHistorySourceRead(ctx context.Context, reader DetailReader, req DetailRequest, version Memory, authorize func(context.Context) error) {
	trackVersionedRead(ctx, reader, req, version, authorize, true)
}

func trackVersionedRead(ctx context.Context, reader DetailReader, req DetailRequest, version Memory, authorize func(context.Context) error, historySource bool) {
	reads, _ := ctx.Value(runReadsKey{}).(*RunReads)
	if reads == nil {
		return
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	if version.ConsentIdentity != "" {
		read := ConsentRead{BotID: req.BotID, GroupID: req.TargetID, ConversationID: req.ConversationID, Kind: req.Kind,
			JobID: ReplyJobFrom(ctx).ID, Claim: ReplyJobFrom(ctx).Claim, MemoryID: req.MemoryID,
			FactVersion: version.AuthorizationVersion, Identity: version.ConsentIdentity}
		if !slices.Contains(reads.crossGroup, read) {
			reads.crossGroup = append(reads.crossGroup, read)
		}
	} else {
		read := LocalRead{DetailRequest: req, FactVersion: version.AuthorizationVersion, UpdatedAt: version.UpdatedAt,
			SourceMessageID: version.SourceMessageID, SourceSenderID: version.SourceSenderID, HistorySource: historySource}
		if !slices.Contains(reads.local, read) {
			reads.local = append(reads.local, read)
		}
		dependency := ReadDependency{ID: req.MemoryID, UpdatedAt: version.UpdatedAt}
		if historySource {
			dependency.SourceMessageID = version.SourceMessageID
		}
		if !slices.Contains(reads.dependencies, dependency) {
			reads.dependencies = append(reads.dependencies, dependency)
		}
	}
	reads.checks = append(reads.checks, func(call context.Context) error {
		if reader == nil || version.UpdatedAt.IsZero() {
			return ErrReadChanged
		}
		if authorize != nil && authorize(call) != nil {
			return ErrReadChanged
		}
		current, found, err := reader.RecallDetail(call, req)
		if err != nil || !found || !current.UpdatedAt.Equal(version.UpdatedAt) || version.AuthorizationVersion != 0 && current.AuthorizationVersion != version.AuthorizationVersion || current.ConsentIdentity != version.ConsentIdentity || historySource && current.SourceMessageID != version.SourceMessageID {
			return ErrReadChanged
		}
		return nil
	})
}

// ValidateCrossGroupOutput guards a derived reply before a platform write.
// ValidateCrossGroupOutput 在写至平台前复核跨群事实派生回复。
func ValidateCrossGroupOutput(ctx context.Context) error {
	if !CrossGroupReadUsed(ctx) {
		return nil
	}
	return ValidateRunReads(ctx)
}

// ValidateRunReads rechecks every memory disclosure before a generation request.
// ValidateRunReads 在生成请求前重新核验每次记忆披露。
func ValidateRunReads(ctx context.Context) error {
	reads, _ := ctx.Value(runReadsKey{}).(*RunReads)
	if reads == nil {
		return nil
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	for _, validate := range reads.checks {
		if err := validate(ctx); err != nil {
			return err
		}
	}
	return nil
}
