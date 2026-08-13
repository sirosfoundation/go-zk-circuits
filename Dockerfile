# Multi-stage build, adapted from go-trust's Dockerfile (circuit-distribution-service-spec.md
# Appendix C). Key deviation: CGO_ENABLED=0 throughout. go-trust's CGO=1 is
# for a PKCS#11 dependency it doesn't itself call directly (a stale
# rationale even there); this service has no such dependency at all, so a
# fully static pure-Go binary and a distroless/scratch runtime are
# available — smaller attack surface, faster builds, no libc dependency.

# Build stage
FROM golang:1.26.5-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev

RUN go install github.com/swaggo/swag/cmd/swag@v1.16.6 \
    && swag init -g cmd/zkc/main.go --output docs/swagger

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -trimpath -o zkc ./cmd/zkc
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -trimpath -o circuitctl ./cmd/circuitctl

# Runtime stage — distroless: no shell, no package manager, minimal attack surface.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /app/zkc /app/zkc
COPY --from=builder /app/circuitctl /app/circuitctl

EXPOSE 8080

ENTRYPOINT ["/app/zkc"]
