# Default task runs the full verification.
# 默认任务执行完整验证。
default: verify

# Run the bot locally against the given config file.
# 本地运行机器人，可指定配置文件。
run config="configs/config.yaml":
    go run ./cmd --config {{config}}

# Build the release binary into bin/.
# 构建二进制到 bin/ 目录。
build:
    go build -o bin/qqbot ./cmd

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
