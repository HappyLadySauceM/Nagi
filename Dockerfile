FROM golang:1.26-alpine AS build
ARG HTTP_PROXY
ARG HTTPS_PROXY
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nagi ./cmd

FROM alpine:3.22
COPY --from=build /out/nagi /usr/local/bin/nagi
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/nagi"]
