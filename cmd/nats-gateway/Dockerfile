# Build the NATS gateway binary
FROM golang:1.27 AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/nats-gateway cmd/nats-gateway
COPY api api
COPY internal internal

RUN CGO_ENABLED=0 GOOS=linux go build -a -o nats-gateway cmd/nats-gateway/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/nats-gateway .
USER 65532:65532
ENTRYPOINT ["/nats-gateway"]
