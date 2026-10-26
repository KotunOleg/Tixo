TEST_ENV = TEST_DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo_test?sslmode=disable \
	TEST_REDIS_URL=redis://localhost:56379/1 \
	TEST_AMQP_URL=amqp://tixo:tixo@localhost:55672/

.PHONY: up down infra run seed test test-integration lint gen gen-check check

up:
	docker compose up --build -d

down:
	docker compose down

infra:
	@test -f compose.override.yml || cp compose.override.example.yml compose.override.yml
	docker compose up -d postgres redis rabbitmq

run:
	DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable \
	REDIS_URL=redis://localhost:56379/0 \
	AMQP_URL=amqp://tixo:tixo@localhost:55672/ \
	go run ./cmd/tixo serve

seed:
	DATABASE_URL=postgres://tixo:tixo@localhost:55432/tixo?sslmode=disable \
	REDIS_URL=redis://localhost:56379/0 \
	AMQP_URL=amqp://tixo:tixo@localhost:55672/ \
	go run ./cmd/tixo seed

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
