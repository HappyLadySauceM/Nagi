ARG RUNNER_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/hls-actions-runner:2.338.1-r4@sha256:aa32434b2dde787343c57274be96e6ae5b59c537ea911625b58fa2c3d8b537ba
ARG GO_IMAGE=zot.infra-registry.svc.cluster.local:5000/infrastructure/golang:1.27
FROM ${GO_IMAGE} AS golang
FROM ${RUNNER_IMAGE}
USER root
RUN rm -rf /usr/local/go
COPY --from=golang /usr/local/go /usr/local/go
ARG PNPM_VERSION=12.14.0
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g "pnpm@${PNPM_VERSION}" \
    && go version && node --version && pnpm --version && just --version \
    && ffmpeg -version && ffprobe -version
ENV GOTOOLCHAIN=local
USER runner
