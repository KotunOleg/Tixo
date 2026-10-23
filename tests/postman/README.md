# QA1 — Postman/Newman

Колекція `tixo.postman_collection.json` прогонює наскрізний сценарій REST API (`internal/api`, C5/B1):
venue → event → публікація → категорія квитків → виконавець → лінійка → пошук → видалення, плюс три
негативні кейси (401 без токена, 404 на чужий/неіснуючий organizer, 422 на невалідну venue).

## Локальний прогін

```sh
make newman
```

Піднімає інфру, мігрує, збирає бінарник, стартує сервер, видає токен, ганяє Newman і гасить сервер.

Вручну, проти вже запущеного сервера (`make run` в іншому терміналі):

```sh
TOKEN=$(go run ./cmd/tixo seed-api-token | sed -n 's/^token: //p')
npx --yes newman@6.2.3 run tests/postman/tixo.postman_collection.json \
  -e tests/postman/local.json \
  --env-var token=$TOKEN
```

Колекція сама створює всі дані через API під унікальними іменами (`{{runId}}` — генерується один раз
за прогін), тому повторний запуск проти тієї самої БД не конфліктує з попереднім.
