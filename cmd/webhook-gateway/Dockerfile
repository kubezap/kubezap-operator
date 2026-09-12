# Build the webhook gateway binary
FROM golang:1.25 AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/webhook-gateway cmd/webhook-gateway
COPY api api
COPY internal internal

RUN CGO_ENABLED=0 GOOS=linux go build -a -o webhook-gateway cmd/webhook-gateway/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/webhook-gateway .
USER 65532:65532
ENTRYPOINT ["/webhook-gateway"]
