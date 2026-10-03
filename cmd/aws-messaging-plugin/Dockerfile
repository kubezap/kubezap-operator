# Build the AWS messaging plugin binary
FROM golang:1.27 AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/aws-messaging-plugin cmd/aws-messaging-plugin
COPY api api
COPY internal internal

RUN CGO_ENABLED=0 GOOS=linux go build -a -o aws-messaging-plugin cmd/aws-messaging-plugin/main.go

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/aws-messaging-plugin .
USER 65532:65532
ENTRYPOINT ["/aws-messaging-plugin"]
