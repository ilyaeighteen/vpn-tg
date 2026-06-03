# vpn-tg

Telegram bot for creating 3x-ui clients from an admin-only button UI.

## Features

- Admin-only access.
- Add admins by Telegram ID.
- Add admins by `@username` after the user has sent any message to the bot.
- Remove admins, but never remove the last admin.
- Create a 3x-ui client for a configured inbound by entering only the client email.
- List and delete existing 3x-ui clients for the configured inbound.
- Flexible `.env` configuration.

## Setup

1. Install Go 1.22+.
2. Copy `.env.example` to `.env` and fill in values.
3. Install dependencies:

```bash
go mod tidy
```

4. Run:

```bash
go run ./cmd/bot
```

## Docker

Copy `.env.example` to `.env`, fill in values, then run:

```bash
docker compose up -d --build
```

View logs:

```bash
docker compose logs -f bot
```

Stop:

```bash
docker compose down
```

Admin storage is mounted as a Docker volume at `/data/admins.json`.

## Notes

`INITIAL_ADMIN_IDS` are merged into `ADMINS_FILE` on startup. Keep at least one ID there for the first launch; the bot refuses to start without any admin.

`USERS_FILE` stores Telegram users who have interacted with the bot. This is required for adding admins by `@username`, because Telegram Bot API does not resolve arbitrary usernames to user IDs.

The bot calls these 3x-ui endpoints:

- `GET /panel/api/inbounds/get/:id`
- `POST /panel/api/inbounds/addClient`
- `POST /panel/api/inbounds/:id/delClientByEmail/:email`

3x-ui requests are authorized with `Authorization: Bearer <XUI_API_TOKEN>`.

Client defaults are controlled by the `XUI_CLIENT_*` variables.
