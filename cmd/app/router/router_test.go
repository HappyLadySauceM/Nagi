// Tests for route registration semantics of the management API.
// 管理 API 路由注册语义的测试。
package router

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	intentcontracts "github.com/HappyLadySauceM/Nagi/internal/intent/contracts"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	mediacontracts "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	obapp "github.com/HappyLadySauceM/Nagi/internal/onebot/application"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
	"github.com/HappyLadySauceM/Nagi/pkg/db/dbtest"
	"github.com/google/uuid"
)

func testContext() *svc.ServiceContext {
	return &svc.ServiceContext{
		Config: &config.Config{Server: config.Server{Listen: "127.0.0.1:5080", Enabled: true}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestSettingsRouteExistsWithoutDatabase verifies the settings endpoint is
// registered even when the database is absent, so callers can tell "not ready"
// (503) from "this build has no such route" (404).
// TestSettingsRouteExistsWithoutDatabase 验证未配置数据库时配置接口仍然注册，
// 调用方因此能区分「未就绪」(503) 与「该构建没有这个路由」(404)。
func TestSettingsRouteExistsWithoutDatabase(t *testing.T) {
	handler := New(testContext(), testContext().Logger)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response must be a JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if envelope.Code != http.StatusServiceUnavailable || envelope.Message != "database not configured" {
		t.Fatalf("envelope = %+v", envelope)
	}
}

// TestUnknownRouteStaysNotFound verifies the unconditional settings route did
// not turn every unknown path into a handler response.
// TestUnknownRouteStaysNotFound 验证无条件注册配置路由没有把未知路径变成处理器响应。
func TestUnknownRouteStaysNotFound(t *testing.T) {
	ctx := testContext()
	handler := New(ctx, ctx.Logger)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/not-a-route", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

const attachmentFixtureBody = "synthetic attachment"

type attachmentFixtureArchive struct {
	mediacontracts.InboundArchiveReader
	opens  int
	onOpen func()
}

func (a *attachmentFixtureArchive) Open(context.Context, int64, int64, string) (io.ReadCloser, mediacontracts.InboundAttachment, error) {
	a.opens++
	if a.onOpen != nil {
		a.onOpen()
	}
	return io.NopCloser(strings.NewReader(attachmentFixtureBody)), mediacontracts.InboundAttachment{
		Filename: "fixture.txt", SizeBytes: int64(len(attachmentFixtureBody)),
	}, nil
}

type attachmentFixtureDecisions struct{ intentcontracts.DecisionReader }

func TestInboundAttachmentAuthorization(t *testing.T) {
	const token = "isolated-attachment-fixture"
	svcCtx := testContext()
	svcCtx.Config.Server.TrustedDecisionAudit = true
	svcCtx.Accounts = svc.NewAccounts()
	baseBot := time.Now().UnixMicro()
	for i := range int64(2) {
		bot := baseBot + i
		client := obapp.NewClientService(nil, time.Second, svcCtx.Logger)
		if err := client.BindIdentity(obdomain.BotID(bot)); err != nil {
			t.Fatal(err)
		}
		if err := svcCtx.Accounts.Add(svc.NewAccountConnection(fmt.Sprint(bot), bot, client)); err != nil {
			t.Fatal(err)
		}
	}
	archive := &attachmentFixtureArchive{}
	svcCtx.InboundArchive = archive
	svcCtx.Decisions = attachmentFixtureDecisions{}
	id := uuid.NewString()
	check := func(t *testing.T, handler http.Handler, bearer string, bot, message int64, status, opens int) {
		t.Helper()
		before := archive.opens
		path := fmt.Sprintf("/api/v1/intent/inbound/%d/attachments/%s?bot_id=%d", message, id, bot)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status || archive.opens-before != opens {
			t.Fatalf("status=%d want=%d, archive opens=%d want=%d", rec.Code, status, archive.opens-before, opens)
		}
		if status != http.StatusOK {
			if strings.Contains(rec.Body.String(), attachmentFixtureBody) || rec.Header().Get("Content-Disposition") != "" {
				t.Fatal("denied response contains attachment data")
			}
			return
		}
		if sha256.Sum256(rec.Body.Bytes()) != sha256.Sum256([]byte(attachmentFixtureBody)) ||
			rec.Header().Get("Content-Disposition") != "attachment; filename=fixture.txt" ||
			rec.Header().Get("Content-Type") != "application/octet-stream" ||
			rec.Header().Get("Content-Length") != fmt.Sprint(len(attachmentFixtureBody)) || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("authorized download did not preserve the attachment response")
		}
	}
	t.Run("local actor denied", func(t *testing.T) {
		check(t, New(svcCtx, svcCtx.Logger), "", baseBot, 1, http.StatusUnauthorized, 0)
	})
	svcCtx.Config.Server.AdminToken = token
	t.Run("no live source reader denied", func(t *testing.T) {
		check(t, New(svcCtx, svcCtx.Logger), token, baseBot, 1, http.StatusNotFound, 0)
	})
	t.Run("database source permissions", func(t *testing.T) {
		pool := dbtest.Open(t)
		ctx := context.Background()
		decisions, err := intentinfra.NewDecisionsRepo(pool)
		if err != nil {
			t.Fatal(err)
		}
		svcCtx.Decisions = decisions
		if _, err := pool.Exec(ctx, `INSERT INTO bot_accounts(bot_id, nickname) VALUES ($1,'fixture'),($2,'fixture')`, baseBot, baseBot+1); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, table := range []string{"messages", "conversations", "scope_policies", "bot_accounts"} {
				if _, err := pool.Exec(ctx, "DELETE FROM "+table+" WHERE bot_id IN ($1,$2)", baseBot, baseBot+1); err != nil {
					t.Error(err)
				}
			}
		})
		seed := func(kind string, target int64, history bool) int64 {
			t.Helper()
			conv := uuid.NewString()
			if _, err := pool.Exec(ctx, `INSERT INTO conversations(id,bot_id,kind,target_id,last_message_at) VALUES($1,$2,$3,$4,now())`, conv, baseBot, kind, target); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO scope_policies(bot_id,scope_type,scope_id,enabled,store_messages,allow_history_read) VALUES($1,$2,$3,true,true,$4)`, baseBot, kind, target, history); err != nil {
				t.Fatal(err)
			}
			var message int64
			if err := pool.QueryRow(ctx, `INSERT INTO messages(conversation_id,bot_id,direction,platform_message_id,sender_id,text_content,segments) VALUES($1,$2,'incoming',$3,$3,'','[]') RETURNING id`, conv, baseBot, target).Scan(&message); err != nil {
				t.Fatal(err)
			}
			return message
		}
		group := seed("group", 100, true)
		private := seed("private", 101, true)
		denied := seed("group", 200, false)
		handler := New(svcCtx, svcCtx.Logger)
		for _, tc := range []struct {
			name, bearer  string
			bot, message  int64
			status, opens int
		}{
			{"missing bearer", "", baseBot, group, http.StatusUnauthorized, 0},
			{"invalid bearer", "invalid-fixture", baseBot, group, http.StatusUnauthorized, 0},
			{"wrong bot", token, baseBot + 1, group, http.StatusNotFound, 0},
			{"unknown bot", token, baseBot + 2, group, http.StatusNotFound, 0},
			{"history denied", token, baseBot, denied, http.StatusNotFound, 0},
			{"group download", token, baseBot, group, http.StatusOK, 1},
			{"private download", token, baseBot, private, http.StatusOK, 1},
		} {
			t.Run(tc.name, func(t *testing.T) { check(t, handler, tc.bearer, tc.bot, tc.message, tc.status, tc.opens) })
		}
		archive.onOpen = func() {
			if _, err := pool.Exec(ctx, `UPDATE scope_policies SET allow_history_read=false WHERE bot_id=$1 AND scope_type='group' AND scope_id=100`, baseBot); err != nil {
				t.Fatal(err)
			}
		}
		t.Run("history revoked after opening", func(t *testing.T) { check(t, handler, token, baseBot, group, http.StatusNotFound, 1) })
	})
}
