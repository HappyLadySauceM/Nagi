# Default task runs the full verification.
# 默认任务执行完整验证。
default: verify

# Create configs/config.yaml from the example when absent.
# 本地配置不存在时从示例复制一份。
init-config:
    test -f configs/config.yaml || cp configs/config-example.yaml configs/config.yaml

# Local integration infra: PostgreSQL 18.6, Redis, MinIO (see docker-compose.yml).
# 本地集成测试基础设施。
test-infra-up:
    docker compose up -d postgres db-init redis minio minio-init --wait

test-infra-down:
    docker compose down

# Start backend and frontend in the background after compose is up.
# 拉起 compose 后后台启动后端与前端。
run:
    #!/usr/bin/env bash
    set -euo pipefail
    just test-infra-up
    just init-config
    mkdir -p .run
    RUN_DIR="${RUN_DIR:-.run}"
    if [[ -f "$RUN_DIR/backend.pid" ]] && kill -0 "$(cat "$RUN_DIR/backend.pid")" 2>/dev/null; then
      echo "backend already running (pid $(cat "$RUN_DIR/backend.pid"))"
    else
      go build -o bin/nagi ./cmd
      setsid nohup ./bin/nagi --config configs/config.yaml >>"$RUN_DIR/backend.log" 2>&1 &
      echo $! >"$RUN_DIR/backend.pid"
      echo "backend starting; log $RUN_DIR/backend.log"
    fi
    if [[ -f "$RUN_DIR/frontend.pid" ]] && kill -0 "$(cat "$RUN_DIR/frontend.pid")" 2>/dev/null; then
      echo "frontend already running"
    else
      [[ -d web/node_modules ]] || (cd web && pnpm install)
      (cd web && setsid nohup pnpm dev >>"../$RUN_DIR/frontend.log" 2>&1 & echo $! >"../$RUN_DIR/frontend.pid")
      echo "frontend starting; log $RUN_DIR/frontend.log"
    fi

# Run the backend in the foreground for log watching.
# 前台运行后端，便于观察日志。
run-fg config="configs/config.yaml": init-config
    go run ./cmd --config {{config}}

# Stop the backend and frontend started by `just run`.
# 停止 just run 启动的后端与前端。
exit:
    #!/usr/bin/env bash
    set -euo pipefail
    RUN_DIR="${RUN_DIR:-.run}"
    stop_one() {
      local name="$1" file="$2"
      [[ -f "$file" ]] || return 0
      local pid
      pid="$(cat "$file" 2>/dev/null || true)"
      if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
        sleep 1
        kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
        echo "stopped $name (pid $pid)"
      fi
      rm -f "$file"
    }
    stop_one backend "$RUN_DIR/backend.pid"
    stop_one frontend "$RUN_DIR/frontend.pid"

# Build the release binary into bin/.
# 构建二进制到 bin/ 目录。
build:
    go build -o bin/nagi ./cmd

# Run all unit tests.
# 运行全部单元测试。
test:
    go test ./...

# Full verification: tidy, vet, test, fmt.
# 完整验证：go mod tidy、go vet、go test、go fmt。
verify:
    go mod tidy
    go vet ./...
    go test ./...
    go fmt ./...

# Repository tier: real PostgreSQL integration (PR / CI required).
# 本地未设 NAGI_TEST_DSN 时自动 just test-infra-up 并使用 nagi_test。
verify-repo:
    #!/usr/bin/env bash
    set -euo pipefail
    export NAGI_TEST_USE_DB="${NAGI_TEST_USE_DB:-1}"
    if [[ -z "${NAGI_TEST_DSN:-}" ]]; then
      if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
        echo "verify-repo: set NAGI_TEST_DSN (CI should use CNPG nagi_test)" >&2
        exit 1
      fi
      just test-infra-up
      export NAGI_TEST_DSN="postgres://nagi:nagi-local@127.0.0.1:5434/nagi_test?sslmode=disable"
    fi
    log="$(mktemp)"
    trap 'rm -f "$log"' EXIT
    go vet -tags integration ./...
    set +e
    go test -tags integration -count=1 -timeout=25m ./... 2>&1 | tee "$log"
    status="${PIPESTATUS[0]}"
    set -e
    if grep -q 'dbtest\.Open disabled' "$log" || grep -q 'NAGI_TEST_DSN required when NAGI_TEST_USE_DB=1' "$log"; then
      echo "verify-repo: database integration skipped while USE_DB=1" >&2
      exit 1
    fi
    exit "$status"

# Full integration: same as verify-repo plus live-only tests (FFmpeg, Ark, real model; may skip).
# 全量 integration：含 verify-repo 与 Live 子集（FFmpeg/Ark/真实模型，可 Skip）。
verify-integration:
    go mod tidy
    go vet -tags integration ./...
    go test -tags integration ./...
    go fmt ./...

# Concurrency: race detector on default (non-integration) packages.
# 并发：默认包全仓 -race。
verify-race:
    go test -race -count=1 -timeout=15m ./...

# Coverage baseline report (not a merge gate by default).
# 覆盖率基线报告（默认不作合并硬门槛）。
verify-cover:
    go test -covermode=atomic -coverpkg=./... -coverprofile=/tmp/nagi-cover.out ./...
    go tool cover -func=/tmp/nagi-cover.out | tail -20

# Dependency vulnerability scan.
# 依赖漏洞扫描。
verify-security:
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Initialise compose test infra and configs/config.yaml when missing.
# 启动 compose 测试基础设施，并在缺失时生成配置。
dev-init:
    just test-infra-up
    just init-config