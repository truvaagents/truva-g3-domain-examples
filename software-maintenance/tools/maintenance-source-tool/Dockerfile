FROM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download && GOWORK=off go mod verify

COPY tools/maintenance-source-tool/ ./tools/maintenance-source-tool/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build \
    -trimpath -ldflags='-w -s' \
    -o /out/maintenance-source-tool ./tools/maintenance-source-tool

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 10001 -S appgroup && \
    adduser -u 10001 -S appuser -G appgroup
WORKDIR /app

COPY --from=builder /out/maintenance-source-tool ./maintenance-source-tool
COPY profiles/ ./profiles/

USER 10001:10001
ENV PORT=8399
EXPOSE 8399
ENTRYPOINT ["/app/maintenance-source-tool"]
