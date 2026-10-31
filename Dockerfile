# 两阶段构建：构建阶段在镜像内编译，运行阶段只放二进制。
# 基础镜像默认走集群内 Zot（runner 的 dind 拉不到 Docker Hub），本地可 --build-arg 覆盖。
# 不写 # syntax= 指令：buildx 自带的前端已支持所用指令，
# 而自定义语法镜像要从 Docker Hub 拉，runner 上会失败。
ARG GO_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/golang:1.27
ARG BASE_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/alpine:3.22

FROM ${GO_IMAGE} AS build

ENV CGO_ENABLED=0
# 只用镜像内的工具链，避免 GOTOOLCHAIN=auto 静默去 proxy 下另一个 Go 版本。
ENV GOTOOLCHAIN=local
# 依赖走国内代理，与本地 go env 及 CI 的 GOPROXY 设置保持一致。
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

# 依赖单独成层：go.mod/go.sum 不变时这层直接命中 buildx 的 gha 层缓存。
# 不挂 GOMODCACHE 的 cache mount：缓存挂载不随 type=gha 导出，CI 的 builder
# 每次都是新建的，而且挂载会遮住上一层已下载的模块，反而让 go build 重新下载。
COPY go.mod go.sum ./
RUN go mod download

# 版本信息由 CI 通过 build-arg 注入；默认值保持本地直接 docker build 的原有行为。
# 声明位置刻意放在依赖层之后：ARG 一旦被某条指令引用就进入该指令的缓存键，
# 把每次提交都变的 COMMIT/BUILD_DATE 放到 go mod download 之后，依赖层就不会被它打穿。
# BuildDate 用提交时间而非当前时间：同一提交重复构建仍命中编译缓存。
ARG VERSION=0.1.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# 只复制编译需要的目录，前端代码变化不会让编译层失效。
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
# 只挂 GOCACHE：编译中间产物（冷编译约 500MB）留在 builder 本地，不进镜像层、
# 也就不会被 mode=max 导出到 gha 缓存；模块仍在上面的层里，可被层缓存复用。
# 显式指定 GOCACHE，避免基础镜像以后改 HOME 时挂载目标与 Go 的默认路径悄悄错开。
ENV GOCACHE=/root/.cache/go-build
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath \
    -ldflags="-s -w \
      -X github.com/HappyLadySauceM/Nagi/pkg/version.Version=${VERSION} \
      -X github.com/HappyLadySauceM/Nagi/pkg/version.Commit=${COMMIT} \
      -X github.com/HappyLadySauceM/Nagi/pkg/version.BuildDate=${BUILD_DATE}" \
    -o /out/nagi ./cmd

FROM ${BASE_IMAGE}
COPY --from=build /out/nagi /usr/local/bin/nagi
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/nagi"]
