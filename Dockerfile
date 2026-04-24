FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -trimpath -o vault-kv-diff .

FROM alpine:3.21
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/vault-kv-diff .
COPY config.yaml .
EXPOSE 9090
CMD ["./vault-kv-diff"]
