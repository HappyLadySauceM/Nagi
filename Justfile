# Default task runs the full verification.
# 默认任务执行完整验证。
default: verify

# Create configs/config.yaml from the example when absent.
# 本地配置不存在时从示例复制一份。
init-config:
    test -f configs/config.yaml || cp configs/config-example.yaml configs/config.yaml

# Start backend and frontend in the background (compose is pulled up when needed).
# 后台启动后端与前端（compose 未启动时自动拉起）。
run:
    scripts/dev-env.sh run

# Run the backend in the foreground for log watching.
# 前台运行后端，便于观察日志。
run-fg config="configs/config.yaml": init-config
    go run ./cmd --config {{config}}

# Stop the backend and frontend started by `just run`.
# 停止 just run 启动的后端与前端。
exit:
    scripts/dev-env.sh exit

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

# Initialise the local dev stack and generate configs/config.yaml when missing.
# 初始化本地开发环境：启动 compose、缺失时生成配置。
dev-init:
    scripts/dev-env.sh init

# Show local dev data volume across PostgreSQL, ClickHouse and the running bot.
# 查看本地开发数据量（PG、ClickHouse、运行中的机器人）。
dev-status:
    scripts/dev-env.sh status

# Clean dev data; dry-run by default, extra flags are forwarded.
# 清理开发数据；默认 dry-run，可透传 --yes/--with-scopes/--reset-volumes。
dev-clean *args:
    scripts/dev-env.sh clean {{args}}
