package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// RequirePermission checks both the run grant and the live source policy.
// RequirePermission 同时检查运行授予与来源会话的实时策略。
func RequirePermission(ctx context.Context, permission string) error {
	scope, ok := ScopeFrom(ctx)
	if !ok || !scope.Permissions[permission] {
		return Denied("缺少权限：" + permission)
	}
	if scope.Reauthorize != nil {
		if err := scope.Reauthorize(ctx, contracts.ToolDescriptor{ID: permission, Permission: permission}); err != nil {
			return Denied("权限已撤销：" + permission)
		}
		return nil
	}
	return nil
}

var contentLink = regexp.MustCompile(`(?i)(https?://|www\.|ftp://)`)

// CheckMessageContent validates decoded segments and returns their budget cost.
// CheckMessageContent 校验解码后的消息段并返回预算字符消耗。
func CheckMessageContent(ctx context.Context, c ob.PublicCommand) (int, error) {
	segments := c.Segments
	if c.Text != nil {
		if c.AutoEscape {
			segments = []ob.Segment{ob.Text(*c.Text)}
		} else {
			segments = ob.ParseCQ(*c.Text)
		}
	}
	raw, err := json.Marshal(ob.WireSegments(segments))
	if err != nil || len(raw) > 65536 {
		return 0, fmt.Errorf("消息超过 64 KiB")
	}
	return checkSegments(ctx, segments, 0)
}
func checkSegments(ctx context.Context, segments []ob.Segment, depth int) (int, error) {
	if depth > 16 {
		return 0, fmt.Errorf("消息嵌套过深")
	}
	cost := 0
	for _, seg := range segments {
		if seg.Type == ob.SegText {
			cost += utf8.RuneCountInString(seg.Data["text"])
		} else {
			cost++
		}
		switch seg.Type {
		case ob.SegText, ob.SegFace, ob.SegReply:
		case ob.SegAt:
			if seg.Data["qq"] == "all" {
				if err := RequirePermission(ctx, "qq.content.at_all"); err != nil {
					return 0, Denied("需要 @全体 内容权限")
				}
			}
		default:
			if err := RequirePermission(ctx, "qq.content.media"); err != nil {
				return 0, Denied("需要媒体内容权限")
			}
		}
		for key, value := range seg.Data {
			if seg.Type == ob.SegNode && key == "content" {
				var text string
				var nested []ob.Segment
				var err error
				if json.Unmarshal([]byte(value), &text) == nil {
					nested = ob.ParseCQ(text)
				} else {
					nested, err = ob.SegmentsFromWire([]byte(value))
					if err != nil {
						nested = ob.ParseCQ(value)
					}
				}
				n, err := checkSegments(ctx, nested, depth+1)
				if err != nil {
					return 0, err
				}
				cost += n
				continue
			}
			decoded := value
			for i := 0; i < 3; i++ {
				v, err := url.QueryUnescape(decoded)
				if err != nil || v == decoded {
					break
				}
				decoded = v
			}
			if contentLink.MatchString(decoded) || key == "url" && strings.TrimSpace(decoded) != "" {
				if err := RequirePermission(ctx, "qq.content.links"); err != nil {
					return 0, Denied("需要链接内容权限")
				}
			}
			if seg.Type != ob.SegText && key == "text" {
				cost += utf8.RuneCountInString(value)
			}
			if strings.Contains(decoded, "[CQ:") && !(seg.Type == ob.SegText && key == "text") {
				nested := ob.ParseCQ(decoded)
				n, err := checkSegments(ctx, nested, depth+1)
				if err != nil {
					return 0, err
				}
				cost += n
			}
		}
	}
	return cost, nil
}

type permissionDenial struct{ message string }

func (e permissionDenial) Error() string       { return e.message }
func (e permissionDenial) ToolStatus() string  { return StatusDenied }
func (e permissionDenial) UserMessage() string { return e.message }

// ErrorCode attributes the refusal to a revoked authorization.
// ErrorCode 把拒绝归因为授权被撤销。
func (e permissionDenial) ErrorCode() string { return "authorization_revoked" }

// Denied returns a safe authorization failure for the tool guard.
// Denied 向工具守卫返回可安全展示的授权失败。
func Denied(message string) error { return permissionDenial{message} }
