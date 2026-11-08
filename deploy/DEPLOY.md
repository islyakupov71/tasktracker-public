# Развёртывание tasktracker

Локальная демонстрация без Telegram и сетевых туннелей:

```sh
go run ./cmd/tasktracker -dev -tunnel=false -db ./data/demo.db
```

Откройте `http://127.0.0.1:8099`. Данные демонстрации создаются локально.

Для собственного стенда соберите бинарный файл `go build -o bin/tasktracker ./cmd/tasktracker`.
Создайте пользователя `tasktracker`, разместите приложение в `/opt/tasktracker`,
а настройки из `etc/tasktracker.env.example` — в игнорируемом файле
`/opt/tasktracker/etc/tasktracker.env` с правами 600. Укажите собственный токен.

При использовании HTTPS-прокси укажите `MINIAPP_URL=https://tasks.example.com`
и `TUNNEL=off`; HTTP-сервер оставьте на `127.0.0.1:8099`.
В `tasktracker.service` используется отдельный пользователь `tasktracker`.

Участники обозначены как «Участник 01» и «Участник 02». Для пингов Telegram
и команды `/my` задайте `MEMBER_ONE_USERNAME` и `MEMBER_TWO_USERNAME` только
в локальном окружении. Пустые значения никого не идентифицируют.
