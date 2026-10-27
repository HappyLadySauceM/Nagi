# 运行态镜像：二进制由 CI 在 runner 内编译，这里只打包。
FROM alpine:3.22
COPY --chmod=0755 bin/nagi /usr/local/bin/nagi
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/nagi"]
