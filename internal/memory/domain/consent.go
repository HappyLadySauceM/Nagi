package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

func ConsentPrompt(id, fact string, groups []int64) string {
	targets := make([]string, len(groups))
	for i, group := range groups {
		targets[i] = strconv.FormatInt(group, 10)
	}
	return fmt.Sprintf("跨群记忆确认 %s\n事实：%s\n目标群：%s\n仅在这些群回复你本人时使用。群友可以看到回复。\n同意请在原群引用本消息发送「同意跨群使用」。撤回请引用发送「撤回全部」或「撤回群 <群号>」，也可直接发送「撤回跨群 %s 全部」或「撤回跨群 %s <群号>」。", id, strconv.Quote(fact), strings.Join(targets, "、"), id, id)
}

func ConsentPromptDigest(prompt string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(prompt)))
}

const ConsentPromptKind = "memory_consent_prompt"
const ConsentCommandKind = "memory_consent_command"

// ConsentCommand is a deterministic control message from the original group.
// ConsentCommand 是来自原群的确定性控制消息。
type ConsentCommand struct {
	ProposalID    string
	ReplyID       int64
	Confirm       bool
	TargetGroupID int64
}

// ParseConsentCommand distinguishes malformed control text from ordinary chat.
// ParseConsentCommand 区分格式有误的控制消息与普通对话。
func ParseConsentCommand(botID int64, segments []obdomain.Segment, fallback string) (ConsentCommand, bool, bool) {
	var text strings.Builder
	var cmd ConsentCommand
	valid := true
	mentions := 0
	for _, segment := range segments {
		switch string(segment.Type) {
		case "text":
			text.WriteString(segment.Data["text"])
		case "reply":
			id, err := strconv.ParseInt(segment.Data["id"], 10, 64)
			if cmd.ReplyID != 0 || err != nil || id == 0 {
				valid = false
			}
			cmd.ReplyID = id
		case "at":
			mentions++
			valid = valid && mentions == 1 && segment.Data["qq"] == strconv.FormatInt(botID, 10)
		default:
			valid = false
		}
	}
	body := strings.TrimSpace(text.String())
	if len(segments) == 0 {
		body = strings.TrimSpace(fallback)
		valid = false
	}
	target := func(raw string) bool {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || raw != strconv.FormatInt(id, 10) {
			return false
		}
		cmd.TargetGroupID = id
		return true
	}
	switch {
	case body == "同意跨群使用":
		cmd.Confirm = true
		return cmd, true, valid && cmd.ReplyID != 0
	case body == "撤回全部":
		return cmd, true, valid && cmd.ReplyID != 0
	case strings.HasPrefix(body, "撤回群 "):
		ok := target(strings.TrimPrefix(body, "撤回群 "))
		return cmd, true, valid && cmd.ReplyID != 0 && ok
	case strings.HasPrefix(body, "撤回跨群 "):
		fields := strings.Split(body, " ")
		if len(fields) != 3 {
			return cmd, true, false
		}
		id, err := uuid.Parse(fields[1])
		if err != nil || fields[1] != id.String() {
			return cmd, true, false
		}
		cmd.ProposalID = id.String()
		ok := fields[2] == "全部" || target(fields[2])
		return cmd, true, valid && cmd.ReplyID == 0 && mentions == 0 && ok
	case strings.HasPrefix(body, "同意跨群使用") || strings.HasPrefix(body, "撤回全部") || strings.HasPrefix(body, "撤回群") || strings.HasPrefix(body, "撤回跨群"):
		return cmd, true, false
	default:
		return cmd, false, false
	}
}

// DecodeConsentCommand verifies persisted OneBot segments using the same parser.
// DecodeConsentCommand 使用同一解析器核验已持久化的 OneBot 消息段。
func DecodeConsentCommand(botID int64, encoded []byte) (ConsentCommand, bool) {
	var segments []obdomain.Segment
	if json.Unmarshal(encoded, &segments) != nil {
		return ConsentCommand{}, false
	}
	cmd, _, valid := ParseConsentCommand(botID, segments, "")
	return cmd, valid
}
