FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/vpn-tg-bot ./cmd/bot

FROM alpine:3.20

RUN addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /out/vpn-tg-bot /app/vpn-tg-bot

ENV ADMINS_FILE=/data/admins.json
ENV USERS_FILE=/data/users.json

RUN mkdir -p /data && chown -R app:app /data /app
USER app

ENTRYPOINT ["/app/vpn-tg-bot"]
