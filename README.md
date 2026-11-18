# gigafin

Self-hosted personal finance manager. Go + SQLite, server-rendered UI (templ + htmx), English and Russian.

## Features
- **Operations** — income, expense, transfers; split, tags, filters.
- **Accounts** — debit, credit cards, cash, savings, loans/debts; balance adjustment, archive.
- **Categories** — nested income/expense categories.
- **Rules** — auto-categorisation (payee conditions → category, rename payee), preview, learn from history.
- **Plans** — monthly income/expense plans with auto-repeat.
- **Analytics** — monthly totals, income vs expense trends, category breakdown, net worth, top payees.
- **Import** — T-Bank, ZenMoney and gigafin CSV, with undo; CBR exchange rates.
- **Bank sync** — T-Bank background sync with balance check.
- **Data** — CSV export, SQLite backup (admin), delete all data.
- **API** — gigafin JSON API (`/api`, OpenAPI at `/api/openapi.yaml`, docs at `/api/docs`) and MCP server (`/mcp`), both via bearer API keys.
- **Security** — password login, session management, revocable API keys.

## Quick start
```sh
make tools
make user-add login=me role=admin
make run            # migrates gigafin.db, serves on 127.0.0.1:8080
```
Config via env (`DB_PATH`, `HTTP_ADDR`, `BANKS_SECRET_KEY`, `TOTP_SECRET_KEY`, …), see `internal/config`.

## CLI
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
