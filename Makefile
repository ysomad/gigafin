-include .env
DOTENV = set -a; [ ! -f .env ] || . ./.env; set +a;

DB ?= $(or $(DB_PATH),gigafin.db)
ADDR ?= $(or $(HTTP_ADDR),127.0.0.1:8080)
TOOL = go tool
GOOSE = $(TOOL) goose -dir migrations sqlite3 $(DB)

.PHONY: tools generate build build-app build-cli run dev test lint vuln locales migrate-up migrate-down migrate-status migrate-create \
	user-add user-passwd user-role user-2fa-off clean

tools:
	go mod download

generate:
	$(TOOL) sqlc generate -f .sqlc.yaml
	$(TOOL) ogen --config .ogen.yaml --target internal/gen/ogen --package ogen --clean api/openapi.yaml
	$(TOOL) templ generate -path internal/ui
	$(TOOL) esbuild web/js/main.js web/css/main.css --bundle --minify --outdir=web/static --entry-names=app.min

build: build-app build-cli

build-app:
	go build -o bin/gigafin ./cmd/app

build-cli:
	go build -o bin/gigafin-cli ./cmd/cli

run: build-app migrate-up
	$(DOTENV) DB_PATH=$(DB) HTTP_ADDR=$(ADDR) ./bin/gigafin | hl -P --config=.hl.yaml

dev:
	$(DOTENV) DB_PATH=$(DB) HTTP_ADDR=$(ADDR) HTTP_STATIC_CACHE_TTL=0 $(TOOL) air | hl -P --config=.hl.yaml

test:
	go test -race ./...

lint: vuln locales
	golangci-lint run ./...

# locales validates the UI dictionaries against their JSON schema
locales:
	check-jsonschema --schemafile web/locales/schema.json $(filter-out web/locales/schema.json,$(wildcard web/locales/*.json))

vuln:
	$(TOOL) govulncheck ./...

migrate-up:
	$(GOOSE) up

migrate-down:
	$(GOOSE) down

migrate-status:
	$(GOOSE) status

migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=<name>" && exit 1)
	$(TOOL) goose -dir migrations -s create $(name) sql

user-add: build-cli
	@test -n "$(login)" || (echo "usage: make user-add login=<login> [role=<user|admin>] [p=<password>]" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin-cli user add -login $(login) -role $(or $(role),user)$(if $(p), -p '$(p)')

user-passwd: build-cli
	@test -n "$(login)" || (echo "usage: make user-passwd login=<login> [p=<password>] [revoke_keys=1]" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin-cli user passwd -login $(login)$(if $(p), -p '$(p)')$(if $(revoke_keys), -revoke-keys)

user-role: build-cli
	@test -n "$(login)" -a -n "$(role)" || (echo "usage: make user-role login=<login> role=<user|admin>" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin-cli user role -login $(login) -role $(role)

user-2fa-off: build-cli
	@test -n "$(login)" || (echo "usage: make user-2fa-off login=<login>" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin-cli user 2fa-off -login $(login)

clean:
	rm -rf bin
