TEST_ENV = TEST_DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo_test?sslmode=disable \
	TEST_REDIS_URL=redis://localhost:56379/1 \
	TEST_AMQP_URL=amqp://tixo:tixo@localhost:55672/

.PHONY: up down infra run seed import-mb test test-integration lint gen gen-check check newman

up:
	docker compose up --build -d

down:
	docker compose down

infra:
	@test -f compose.override.yml || cp compose.override.example.yml compose.override.yml
	docker compose up -d postgres redis rabbitmq

run:
	@if [ -f .env ]; then . ./.env; fi; \
	GOOGLE_CLIENT_ID="$$GOOGLE_CLIENT_ID" GOOGLE_CLIENT_SECRET="$$GOOGLE_CLIENT_SECRET" \
	DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable \
	REDIS_URL=redis://localhost:56379/0 \
	AMQP_URL=amqp://tixo:tixo@localhost:55672/ \
	go run ./cmd/tixo serve

seed:
	DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable \
	REDIS_URL=redis://localhost:56379/0 \
	AMQP_URL=amqp://tixo:tixo@localhost:55672/ \
	go run ./cmd/tixo seed $(foreach o,$(OWNERS),--owner $(o))

import-mb:
	@test -n "$(FILE)" || { echo "usage: make import-mb FILE=path/to/mbdump.tar.bz2"; exit 2; }
	DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable \
	REDIS_URL=redis://localhost:56379/0 \
	AMQP_URL=amqp://tixo:tixo@localhost:55672/ \
	go run ./cmd/tixo import-musicbrainz "$(FILE)"

test:
	go test -race -count=1 ./...

test-integration: infra
	$(TEST_ENV) go test -race -count=1 ./...

lint:
	go tool golangci-lint run ./...

gen:
	go tool sqlc generate

gen-check:
	go tool sqlc diff

check: lint gen-check test-integration

newman: infra
	DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable \
	REDIS_URL=redis://localhost:56379/0 \
	AMQP_URL=amqp://tixo:tixo@localhost:55672/ \
	bash -c 'set -e; \
		bin=$$(mktemp); \
		go build -o "$$bin" ./cmd/tixo; \
		"$$bin" migrate up; \
		if "$$bin" probe >/dev/null 2>&1; then echo "another server already answers the probe; stop it (make down?) before make newman" >&2; exit 1; fi; \
		"$$bin" serve & \
		server=$$!; \
		trap "kill $$server 2>/dev/null; rm -f $$bin" EXIT; \
		ready=; \
		for i in $$(seq 1 30); do \
			kill -0 $$server 2>/dev/null || { echo "serve exited before becoming ready" >&2; exit 1; }; \
			"$$bin" probe >/dev/null 2>&1 && { ready=1; break; }; \
			sleep 1; \
		done; \
		[ -n "$$ready" ] || { echo "serve not ready after 30s" >&2; exit 1; }; \
		token=$$("$$bin" seed-api-token | grep "^token: " | cut -d" " -f2); \
		npx --yes newman run tests/postman/tixo.postman_collection.json -e tests/postman/local.json --env-var token=$$token'
