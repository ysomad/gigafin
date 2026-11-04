# optional KEY=value config, see internal/config; read here for DB and ADDR and exported
# only to commands that run the app, so tests do not see it
-include .env
DOTENV = set -a; [ ! -f .env ] || . ./.env; set +a;

DB ?= $(or $(DB_PATH),gigafin.db)
ADDR ?= $(or $(HTTP_ADDR),127.0.0.1:8080)
TOOL = go tool
GOOSE = $(TOOL) goose -dir migrations sqlite3 $(DB)

.PHONY: tools generate build run dev test lint vuln migrate-up migrate-down migrate-status migrate-create \
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

# run with live reload, see .air.toml
dev:
	$(DOTENV) DB_PATH=$(DB) HTTP_ADDR=$(ADDR) HTTP_STATIC_CACHE_TTL=0 HTTP_API_DOCS=true $(TOOL) air | hl -P --config=.hl.yaml

test:
	go test -race ./...

lint: vuln
	golangci-lint run ./...

vuln:
	$(TOOL) govulncheck ./...

migrate-up:
	$(GOOSE) up

migrate-down:
	$(GOOSE) down

migrate-status:
	$(GOOSE) status

# make migrate-create name=add_tags
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=<name>" && exit 1)
	$(TOOL) goose -dir migrations -s create $(name) sql

# make user-add login=alice [role=admin] [p=secret] (prompts for the password without p)
user-add: build
	@test -n "$(login)" || (echo "usage: make user-add login=<login> [role=<user|admin>] [p=<password>]" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin user add -login $(login) -role $(or $(role),user)$(if $(p), -p '$(p)')

# make user-passwd login=alice [p=secret] (prompts for the new password without p)
user-passwd: build
	@test -n "$(login)" || (echo "usage: make user-passwd login=<login> [p=<password>]" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin user passwd -login $(login)$(if $(p), -p '$(p)')

# make user-role login=alice role=admin
user-role: build
	@test -n "$(login)" -a -n "$(role)" || (echo "usage: make user-role login=<login> role=<user|admin>" && exit 1)
	$(DOTENV) DB_PATH=$(DB) ./bin/gigafin user role -login $(login) -role $(role)

clean:
	rm -rf bin
