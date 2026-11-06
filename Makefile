-include .env
DOTENV = set -a; [ ! -f .env ] || . ./.env; set +a;

DB ?= $(or $(DB_PATH),gigafin.db)
ADDR ?= $(or $(HTTP_ADDR),127.0.0.1:8080)
TOOL = go tool
GOOSE = $(TOOL) goose -dir migrations sqlite3 $(DB)

.PHONY: tools generate build run dev test lint vuln locales migrate-up migrate-down migrate-status migrate-create \
	user-add user-passwd user-role clean

tools:
	go mod download

generate:
	$(TOOL) sqlc generate
	$(TOOL) ogen --config ogen.yml --target internal/gen/ogen --package ogen --clean api/public.yaml

build:
	go build -tags apidocs -o bin/gigafin .

run: build migrate-up
	$(DOTENV) DB_PATH=$(DB) HTTP_ADDR=$(ADDR) HTTP_API_DOCS=true ./bin/gigafin serve | hl -P --config=.hl.yaml

dev:
	$(DOTENV) DB_PATH=$(DB) HTTP_ADDR=$(ADDR) HTTP_STATIC_CACHE_TTL=0 HTTP_API_DOCS=true $(TOOL) air | hl -P --config=.hl.yaml

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

user-add: build
	@test -n "$(login)" || (echo "usage: make user-add login=<login> [role=<user|admin>] [p=<password>]" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin user add -login $(login) -role $(or $(role),user)$(if $(p), -p '$(p)')

user-passwd: build
	@test -n "$(login)" || (echo "usage: make user-passwd login=<login> [p=<password>]" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin user passwd -login $(login)$(if $(p), -p '$(p)')

user-role: build
	@test -n "$(login)" -a -n "$(role)" || (echo "usage: make user-role login=<login> role=<user|admin>" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin user role -login $(login) -role $(role)

clean:
	rm -rf bin
