<p align="center">
  <img src="logo.jpg" alt="logo" width="300">
</p>

<h1 align="center">shampoore-core</h1>

<p align="center">
  <em>Ядро модульного прокси-туннеля поверх кастомных сервисов.</em>
</p>

---

## Что это

**shampoore-core** - ядро для организации сетевого туннеля через публичные сервисы, к которым обычно нет ограничений доступа.

Транспорт, мультиплексор, шифрование и входные интерфейсы разведены по слоям так, чтобы добавление нового транспорта не требовало изменений в других слоях. Транспорт - это реализация одного интерфейса `Pipe`. Всё остальное - универсальное.

## Существующие транспорты

- Yandex Board

## Как это работает

```
Приложения -> (SOCKS5) -> shampoore-client -> кастомный транспорт-сервис -> shampoore-server -> Интернет
Приложения -> (TUN)    -> shampoore-client -> кастомный транспорт-сервис -> shampoore-server -> Интернет
```

- **Клиент** принимает трафик либо через локальный SOCKS5 (`127.0.0.1:1080`), либо через полноценный TUN-интерфейс - и гонит его через выбранный транспорт на сервер.
- **Сервер** открывает TCP-соединения к реальным адресам, гоняет данные обратно.
- **Транспорт** - сервис-посредник, через который ходят данные.

## Установка

```bash
git clone https://github.com/Esteviredzu/shampoore-core.git
cd shampoore-core
go mod download
```

## Сборка

```bash
go build -o shampoore-client ./cmd/shampoore-client
go build -o shampoore-server ./cmd/shampoore-server
```

## Настройка

Конфиг - JSON-файл. Один файл - один конфиг.

Пример (`config_yandexboard.json`):

```json
{
  "name": "🇵🇱 Yandex-Polska",
  "encryption_password": "VERY HARD PASSWORD",
  "mode": "tun",
  "yandex": {
    "hash": "yandexboard_hash"
  }
}
```

## Запуск

**На сервере:**

```bash
./shampoore-server -c config_yandexboard.json
```

**На клиенте:**

```bash
# SOCKS5 - без sudo
./shampoore-client -c config_yandexboard.json

# TUN - нужен root, потому что меняем маршруты ядра
sudo ./shampoore-client -c config_yandexboard.json
```

После запуска SOCKS5-прокси доступен на `127.0.0.1:1080`, а при `mode: "tun"` весь системный трафик идёт через туннель.

Проверка:

```bash
# Проверка SOCKS5-порта
curl -4 --socks5-hostname 127.0.0.1:1080 https://ifconfig.me

# При TUN - просто проверить, что интернет работает
curl -4 https://ifconfig.me
```

## Конфиг через URL

Конфиг можно закодировать в одну строку - по аналогии с `vless://` и `ss://`. Сервер генерирует ссылку:

```bash
./shampoore-server -encode -c config_yandexboard.json
```

Клиент запускается прямо с URL:

```bash
sudo ./shampoore-client -c "shampoore://..."
```

## Шифрование

Трафик между клиентом и сервером шифруется симметричным **XChaCha20-Poly1305**. Ключ выводится из пароля через HKDF-SHA256. Пароль задаётся один раз в `config.json` и должен совпадать на обеих сторонах - клиент и сервер с разными паролями не установят соединение.

Опционально сверху навешивается **padding** (выравнивание размера всех сообщений) и **jitter** (случайная задержка перед отправкой). Оба настраиваются в секции `advanced`.

### Как сгенерировать пароль

**Linux / macOS:**

```bash
openssl rand -base64 48
```

**Через Go:**

```bash
go run - <<'EOF'
package main
import ("crypto/rand"; "encoding/base64"; "fmt")
func main() {
    b := make([]byte, 48)
    rand.Read(b)
    fmt.Println(base64.StdEncoding.EncodeToString(b))
}
EOF
```

## Advanced

Все продвинутые настройки живут в секции `advanced`. Поля необязательные - если не указывать, берутся дефолты. Указывайте, только если знаете зачем и почему.

```json
{
  "encryption_password": "VERY_COOL_PASSWORD",
  "mode": "tun",
  "yandex": {
    "hash": "hash"
  },
  "advanced": {
    "mux_flush_ms": 20,
    "mux_flush_threshold": 12000,
    "reliable_window": 64,
    "reliable_chunk": 0,
    "padding_enabled": true,
    "padding_target_size": 14000,
    "jitter_min_ms": 0,
    "jitter_max_ms": 0
  }
}
```

### Что тут что

- **`mux_flush_ms`** - как часто мультиплексор отправляет накопленные кадры.
- **`mux_flush_threshold`** - сколько байт накопить, чтобы отправить кадры немедленно, не дожидаясь `mux_flush_ms`.
- **`reliable_window`** - сколько пакетов может быть в полёте одновременно, не дожидаясь подтверждения.
- **`reliable_chunk`** - размер чанка. 0 - автоматически.
- **`padding_target_size`** - до какого размера выравнивать, в байтах. Верхний предел ограничен максимальным размером сообщения транспорта. Для Yandex это ~15000 байт (maxValueSize в pipe).
- **`jitter`** - Случайная задержка перед каждым исходящим сообщением.

## TUN

TUN пока работает только на Linux потому-что `I use arch BTW`

## Архитектура

```
cmd/                       - cli
internal/app/              - реестр транспортов, Settings, Runtime
internal/multiplexing/     - логические стримы поверх одного канала
internal/socks5/           - SOCKS5-сервер и проксирование стримов в TCP
internal/runtime/          - TUN, tun2socks, интеграция с ядром ОС
internal/transport/        - Pipe, Transport, ReliableSession, декораторы security
internal/transport/yandex/ - конкретный транспорт через Яндекс.Доски
```

---

# Транспорт: Yandex Board

### Где взять хэш

1. Откройте [boards.yandex.ru](https://boards.yandex.ru).
2. Создайте новую доску. **Используйте новую, чистую доску.**
3. **Поделиться** > **Редактирование для всех** > **Включить гостевой доступ** > **Права гостей: редактирование**.
4. Скопируйте хэш из ссылки. Она выглядит так: `https://boards.yandex.ru/whiteboard/?hash=YOUR_HASH`

---

## Статус

Ядро работает.

## О проекте

Изначально проект сделан на Python, но Python для такого проекта - ещё то извращение, так что сделал ядро на Go.

Я очень вдохновился [olcrtc](https://github.com/openlibrecommunity/olcrtc) и [OpenFlux](https://github.com/p1neapplexpress/OpenFlux), и захотел сделать что-то своё)