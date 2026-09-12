# Build the AMQP gateway binary
FROM golang:1.25 AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/amqp-gateway cmd/amqp-gateway
COPY api api
COPY internal internal

RUN CGO_ENABLED=0 GOOS=linux go build -a -o amqp-gateway cmd/amqp-gateway/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/amqp-gateway .
USER 65532:65532
ENTRYPOINT ["/amqp-gateway"]
