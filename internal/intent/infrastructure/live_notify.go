package infrastructure

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// LiveWakeup distributes PostgreSQL hints locally. The journal remains authoritative.
// LiveWakeup 在副本内分发 PostgreSQL 唤醒通知。真实数据仍以持久日志为准。
type LiveWakeup struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]int64
}

func NewLiveWakeup() *LiveWakeup {
	return &LiveWakeup{subscribers: make(map[chan struct{}]int64)}
}

func (w *LiveWakeup) Subscribe(bot int64) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	w.subscribers[ch] = bot
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subscribers, ch)
		w.mu.Unlock()
	}
}

func (w *LiveWakeup) signal(bot int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch, subscriber := range w.subscribers {
		if subscriber == bot {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// Watch owns one dedicated listener per API process, independent of stream count.
// Watch 每个 API 进程仅使用一个监听连接，与流数量无关。
func (w *LiveWakeup) Watch(ctx context.Context, dsn string) {
	for ctx.Err() == nil {
		w.listen(ctx, dsn)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *LiveWakeup) listen(ctx context.Context, dsn string) {
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := pgx.Connect(startup, dsn)
	if err == nil {
		_, err = conn.Exec(startup, `LISTEN intent_live`)
	}
	cancel()
	if conn != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = conn.Close(closeCtx)
		}()
	}
	if err != nil {
		return
	}
	for ctx.Err() == nil {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return
		}
		bot, err := strconv.ParseInt(notification.Payload, 10, 64)
		if err == nil && bot > 0 {
			w.signal(bot)
		}
	}
}
