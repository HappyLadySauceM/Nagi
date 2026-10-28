# 运行态镜像：dist 由 CI 在 runner 内构建，这里只打包。
# 默认走集群内 Zot；本地可 --build-arg BASE_IMAGE=nginx:alpine 覆盖。
ARG BASE_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/nginx:alpine
FROM ${BASE_IMAGE}
COPY dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
