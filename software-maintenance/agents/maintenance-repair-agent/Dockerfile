FROM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download && GOWORK=off go mod verify

COPY agents/maintenance-repair-agent/ ./agents/maintenance-repair-agent/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build \
    -trimpath -ldflags='-w -s' \
    -o /out/maintenance-repair-agent ./agents/maintenance-repair-agent

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 20001 -S repair && \
    adduser -u 10001 -S appuser -G repair
WORKDIR /app

COPY --from=builder /out/maintenance-repair-agent ./maintenance-repair-agent
COPY profiles/ ./profiles/

USER 10001:20001
ENV PORT=8400
EXPOSE 8400
ENTRYPOINT ["/app/maintenance-repair-agent"]
