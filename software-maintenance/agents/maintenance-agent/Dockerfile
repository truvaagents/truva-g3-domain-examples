FROM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download && GOWORK=off go mod verify

COPY agents/maintenance-agent/ ./agents/maintenance-agent/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build \
    -trimpath -ldflags='-w -s' \
    -o /out/maintenance-agent ./agents/maintenance-agent

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 20001 -S coordinator && \
    adduser -u 10001 -S appuser -G coordinator
WORKDIR /app

COPY --from=builder /out/maintenance-agent ./maintenance-agent
COPY profiles/ ./profiles/

USER 10001:20001
ENV PORT=8398
EXPOSE 8398
ENTRYPOINT ["/app/maintenance-agent"]
