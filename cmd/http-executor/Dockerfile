# Build the http-executor binary
FROM golang:1.25 AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/http-executor cmd/http-executor
COPY api api
COPY internal internal

RUN CGO_ENABLED=0 GOOS=linux go build -a -o http-executor cmd/http-executor/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/http-executor .
USER 65532:65532
ENTRYPOINT ["/http-executor"]
