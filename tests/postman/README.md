# QA1 — Postman/Newman

Колекція `tixo.postman_collection.json` прогонює наскрізний сценарій REST API (`internal/api`, C5/B1):
venue → event → публікація → категорія квитків → виконавець → лінійка → пошук → видалення, плюс три
негативні кейси (401 без токена, 404 на чужий/неіснуючий organizer, 422 на невалідну venue).

## Локальний прогін

```sh
make infra
go run ./cmd/tixo migrate up
go run ./cmd/tixo serve &
TOKEN=$(go run ./cmd/tixo seed-api-token | grep '^token: ' | cut -d' ' -f2)
npx newman run tests/postman/tixo.postman_collection.json \
  -e tests/postman/local.json \
  --env-var token=$TOKEN
```

Або одразу: `make newman` (піднімає інфру, мігрує, видає токен, стартує сервер, ганяє Newman, гасить сервер).

Колекція сама створює всі дані через API під унікальними іменами (`{{runId}}` — генерується один раз
за прогін), тому повторний запуск проти тієї самої БД не конфліктує з попереднім.
