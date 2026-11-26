# gigafin

Self-hosted personal finance manager. Go + SQLite, server-rendered UI (templ + htmx), English and Russian.

## Features
- **Operations** — income, expense, transfers; split, tags, filters.
- **Accounts** — debit, credit cards, cash, savings, loans/debts; balance adjustment, archive.
- **Categories** — nested income/expense categories.
- **Rules** — auto-categorisation (payee conditions → category, rename payee), preview, learn from history.
- **Plans** — monthly income/expense plans with auto-repeat.
- **Import** — T-Bank, ZenMoney and gigafin CSV, with undo; CBR exchange rates.
- **Bank sync** — T-Bank background sync with balance check.
- **Data** — CSV export, SQLite backup (admin), delete all data.
- **API** — gigafin JSON API (`/api`, OpenAPI at `/api/openapi.yaml`, docs at `/api/docs`) and MCP server (`/mcp`).
- **Security** — password login, session management, revocable API keys.

## Quick start
```sh
make tools
make user-add login=me role=admin
make run            # serves gigafin.db on 127.0.0.1:8080
```
Config via env, see [.env.example](.env.example).

## Deploy
Download `gigafin` for your platform from [Releases](https://github.com/ysomad/gigafin/releases), or
`go install github.com/ysomad/gigafin@latest`.

```sh
export DB_PATH=/var/lib/gigafin/gigafin.db
export HTTP_ADDR=0.0.0.0:8080
export BANKS_SECRET_KEY=$(openssl rand -base64 32)
export TOTP_SECRET_KEY=$(openssl rand -base64 32)

gigafin user add -login me -role admin
gigafin serve

```
Keep `BANKS_SECRET_KEY` and `TOTP_SECRET_KEY` the same across restarts. Behind HTTPS set
`HTTP_SECURE_COOKIES=true`. All settings: [.env.example](.env.example).

## CLI
`gigafin user add|passwd|role|2fa-off`, or via make:
```sh
make user-add login=alice                 # role=user|admin (default user), p=PASSWORD
make user-passwd login=alice              # change password, ends all sessions; p=PASSWORD
make user-role login=alice role=admin     # role=user|admin
make user-2fa-off login=alice             # turn two-factor auth off, when the app and recovery codes are lost
```

## Development
```sh
make generate       # sqlc, ogen and templ code
make test
make lint
make dev            # live reload
```

See [AGENTS.md](AGENTS.md) and [CODE_STYLE.md](CODE_STYLE.md).

Roadmap: [ROADMAP.md](ROADMAP.md).
