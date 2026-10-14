FROM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download && GOWORK=off go mod verify

COPY tools/maintenance-workspace-tool/ ./tools/maintenance-workspace-tool/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build \
    -trimpath -ldflags='-w -s' \
    -o /out/maintenance-workspace-tool ./tools/maintenance-workspace-tool

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 20001 -S workspace && \
    adduser -u 10001 -S appuser -G workspace
WORKDIR /app

COPY --from=builder /out/maintenance-workspace-tool ./maintenance-workspace-tool
COPY profiles/ ./profiles/

USER 10001:20001
ENV PORT=8401
EXPOSE 8401
ENTRYPOINT ["/app/maintenance-workspace-tool"]
