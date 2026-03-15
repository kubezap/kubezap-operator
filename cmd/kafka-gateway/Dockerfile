# Build the Kafka gateway binary
FROM golang:1.24 AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/kafka-gateway cmd/kafka-gateway
COPY api api
COPY internal internal

RUN CGO_ENABLED=0 GOOS=linux go build -a -o kafka-gateway cmd/kafka-gateway/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/kafka-gateway .
USER 65532:65532
ENTRYPOINT ["/kafka-gateway"]
