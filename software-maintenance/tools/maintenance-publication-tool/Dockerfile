FROM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download && GOWORK=off go mod verify

COPY tools/maintenance-publication-tool/ ./tools/maintenance-publication-tool/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build \
    -trimpath -ldflags='-w -s' \
    -o /out/maintenance-publication-tool ./tools/maintenance-publication-tool

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 20001 -S publication && \
    adduser -u 10001 -S appuser -G publication
WORKDIR /app

COPY --from=builder /out/maintenance-publication-tool ./maintenance-publication-tool
COPY profiles/ ./profiles/

USER 10001:20001
ENV PORT=8403
EXPOSE 8403
ENTRYPOINT ["/app/maintenance-publication-tool"]
