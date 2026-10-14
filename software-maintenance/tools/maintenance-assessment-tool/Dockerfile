FROM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates git
WORKDIR /src

COPY go.mod go.sum ./
RUN GOWORK=off go mod download && GOWORK=off go mod verify

COPY tools/maintenance-assessment-tool/ ./tools/maintenance-assessment-tool/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOWORK=off go build \
    -trimpath -ldflags='-w -s' \
    -o /out/maintenance-assessment-tool ./tools/maintenance-assessment-tool

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 10001 -S appgroup && \
    adduser -u 10001 -S appuser -G appgroup
WORKDIR /app

COPY --from=builder /out/maintenance-assessment-tool ./maintenance-assessment-tool
COPY profiles/ ./profiles/

USER 10001:10001
ENV PORT=8404
EXPOSE 8404
ENTRYPOINT ["/app/maintenance-assessment-tool"]
