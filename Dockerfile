# 运行态镜像：二进制由 CI 在 runner 内编译，这里只打包。
# 默认走集群内 Zot；本地可 --build-arg BASE_IMAGE=alpine:3.22 覆盖。
ARG BASE_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/alpine:3.22
FROM ${BASE_IMAGE}
COPY --chmod=0755 bin/nagi /usr/local/bin/nagi
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/nagi"]
