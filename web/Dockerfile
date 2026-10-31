# 两阶段构建：构建阶段在镜像内 pnpm 构建，运行阶段只放 dist。
# 基础镜像默认走集群内 Zot（runner 的 dind 拉不到 Docker Hub），本地可 --build-arg 覆盖。
ARG NODE_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/node:24-alpine
ARG BASE_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/nginx:alpine

FROM ${NODE_IMAGE} AS build

# 仓库里没有 .npmrc：在构建阶段显式指定镜像源，避免直连 registry.npmjs.org。
ENV NPM_CONFIG_REGISTRY=https://registry.npmmirror.com
ARG PNPM_VERSION=12.14.0
RUN npm install -g "pnpm@${PNPM_VERSION}"
WORKDIR /app

# 依赖单独成层：lockfile 不变时命中缓存，改源码不会重装依赖。
# pnpm-workspace.yaml 声明了 allowBuilds（esbuild），必须和 lockfile 一起进这一层，
# 否则 install 会按「未审批的构建脚本」报错。
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
# store 挂载只加速本地重复构建：它不随 type=gha 导出，CI 上靠的是这一层命中层缓存。
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile

COPY . .
RUN pnpm build

FROM ${BASE_IMAGE}
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
