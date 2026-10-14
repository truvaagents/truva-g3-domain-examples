FROM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download && GOWORK=off go mod verify

COPY agents/maintenance-review-agent/ ./agents/maintenance-review-agent/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build \
    -trimpath -ldflags='-w -s' \
    -o /out/maintenance-review-agent ./agents/maintenance-review-agent

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 20001 -S review && \
    adduser -u 10001 -S appuser -G review
WORKDIR /app

COPY --from=builder /out/maintenance-review-agent ./maintenance-review-agent
COPY profiles/ ./profiles/

USER 10001:20001
ENV PORT=8402
EXPOSE 8402
ENTRYPOINT ["/app/maintenance-review-agent"]
