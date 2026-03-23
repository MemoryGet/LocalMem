FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /iclude ./cmd/server/main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /iclude /usr/local/bin/iclude
COPY config.yaml /etc/iclude/config.yaml

EXPOSE 8080
ENTRYPOINT ["iclude"]
