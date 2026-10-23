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

## Платформы

- **Linux** - CLI, SOCKS5, полноценный TUN.
- **Android** - через `gomobile` собирается в AAR. SOCKS5 и TUN через системный `VpnService` fd.

## Существующие транспорты

- **Yandex Board** - через яндекс доски.
- **Yandex Telemost raw** - через VP8-видео в конференции Телемоста.
- **Yandex Telemost squares** - тоже VP8, но данные рисуются сеткой квадратов в кадре.

## Как это работает

```
Приложения -> (SOCKS5) -> shampoore-client -> кастомный транспорт-сервис -> shampoore-server -> Интернет
Приложения -> (TUN)    -> shampoore-client -> кастомный транспорт-сервис -> shampoore-server -> Интернет
```

- **Клиент** принимает трафик либо через локальный SOCKS5 (`127.0.0.1:1080`), либо через TUN-интерфейс - и гонит его через выбранный транспорт на сервер.
- **Сервер** открывает TCP-соединения к реальным адресам, гоняет данные обратно.
- **Транспорт** - сервис-посредник, через который ходят данные.

## Установка

```bash
git clone https://github.com/Esteviredzu/shampoore-core.git
cd shampoore-core
go mod download
```

## Сборка

### Linux

```bash
go build -o shampoore-client ./cmd/shampoore-client
go build -o shampoore-server ./cmd/shampoore-server
```

### Android (AAR)

Нужны: Go >= 1.23, Android NDK, gomobile.

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init

export ANDROID_NDK_HOME=/path/to/ndk
./build-aar.sh
```

На выходе появятся `shampoore.aar` и `shampoore-sources.jar`

## Настройка

Конфиг - JSON-файл. Один файл - один конфиг.

### Yandex Board

```json
{
  "name": "yandex-board",
  "encryption_password": "VERY HARD PASSWORD",
  "mode": "tun",
  "yandex": {
    "hash": "yandexboard_hash"
  }
}
```

### Yandex Telemost raw

```json
{
  "name": "telemost-raw",
  "encryption_password": "VERY HARD PASSWORD",
  "mode": "tun",
  "transport_kind": "yandex_telemost_raw",
  "yandex_telemost_raw": {
    "conference_link": "https://telemost.yandex.ru/j/XXXXXXXX",
    "display_name": "guest"
  }
}
```

### Yandex Telemost squares

```json
{
  "name": "telemost-squares",
  "encryption_password": "VERY HARD PASSWORD",
  "mode": "tun",
  "transport_kind": "yandex_telemost_squares",
  "yandex_telemost_squares": {
    "conference_link": "https://telemost.yandex.ru/j/XXXXXXXX",
    "display_name": "guest"
  }
}
```

## Запуск

**На сервере:**

```bash
./shampoore-server -c config.json
```

**На клиенте:**

```bash
# SOCKS5 - без sudo
./shampoore-client -c config.json

# TUN - нужен root, потому что меняем маршруты ядра
sudo ./shampoore-client -c config.json
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
./shampoore-server -encode -c config.json
```

Клиент запускается прямо с URL:

```bash
sudo ./shampoore-client -c "shampoore://yandex?hash=...&pw=..."
sudo ./shampoore-client -c "shampoore://yandex_telemost_raw?conf=https://telemost.yandex.ru/j/...&name=guest&pw=..."
```

## Шифрование

Трафик между клиентом и сервером шифруется симметричным **XChaCha20-Poly1305**. Ключ выводится из пароля через HKDF-SHA256. Пароль задаётся один раз в `config.json` и должен совпадать на обеих сторонах - клиент и сервер с разными паролями не установят соединение.

Для Telemost-транспортов дополнительно доступен **FEC** (Reed-Solomon) поверх KCP.

### Как сгенерировать пароль

**Linux / macOS:**

```bash
openssl rand -base64 48
```

## Advanced

Все продвинутые настройки живут в секции `advanced`. Поля необязательные - если не указывать, берутся дефолты. Указывайте, только если знаете зачем и почему.

### Yandex Board

```json
{
  "advanced": {
    "mux_flush_ms": 20,
    "mux_flush_threshold": 12000,
    "reliable_window": 64,
    "reliable_chunk": 0,
    "padding_enabled": true,
    "padding_target": 14000,
    "jitter_min_ms": 0,
    "jitter_max_ms": 0
  }
}
```

- **`mux_flush_ms`** - как часто мультиплексор отправляет накопленные кадры.
- **`mux_flush_threshold`** - сколько байт накопить, чтобы отправить кадры немедленно, не дожидаясь `mux_flush_ms`.
- **`reliable_window`** - сколько пакетов может быть в полёте одновременно, не дожидаясь подтверждения.
- **`reliable_chunk`** - размер чанка. 0 - автоматически.
- **`padding_enabled`** - включить выравнивание размера сообщений.
- **`padding_target`** - до какого размера выравнивать, в байтах. Верхний предел ограничен максимальным размером сообщения транспорта. Для Yandex Board это ~15000 байт.
- **`jitter_min_ms` / `jitter_max_ms`** - случайная задержка перед каждым исходящим сообщением.

### Yandex Telemost raw

```json
{
  "advanced": {
    "mux_flush_ms": 10,
    "mux_flush_threshold": 12000,
    "kcp_conv": 0,
    "kcp_mtu": 1200,
    "kcp_window": 1024,
    "kcp_nodelay": 1,
    "kcp_interval": 10,
    "kcp_resend": 2,
    "kcp_nc": true,
    "fec_enabled": false,
    "fec_data_shards": 10,
    "fec_parity_shards": 3,
    "raw_frame_size": 12000,
    "raw_fps": 60,
    "raw_copies": 1,
    "raw_gop": 30
  }
}
```

### Yandex Telemost (squares)

```json
{
  "advanced": {
    "mux_flush_ms": 10,
    "mux_flush_threshold": 4000,
    "kcp_conv": 0,
    "kcp_mtu": 390,
    "kcp_window": 512,
    "kcp_nodelay": 1,
    "kcp_interval": 5,
    "kcp_resend": 2,
    "kcp_nc": true,
    "fec_enabled": false,
    "fec_data_shards": 10,
    "fec_parity_shards": 3,
    "squares_bits_per_cell": 4,
    "squares_cell_size": 16,
    "squares_cols": 120,
    "squares_rows": 80,
    "squares_fps": 45,
    "squares_bitrate": "4000K",
    "squares_copies": 1
  }
}
```

- **`kcp_conv`** - conv id для KCP. Если 0 - выводится из ссылки на конференцию, так что клиент и сервер автоматически сойдутся.
- **`kcp_mtu`** - максимальный размер пакета KCP. Для squares сильно ниже (390 по умолчанию), потому что кадр несёт мало данных.
- **`kcp_window`, `kcp_nodelay`, `kcp_interval`, `kcp_resend`, `kcp_nc`** - стандартные настройки KCP.
- **`fec_enabled` / `fec_data_shards` / `fec_parity_shards`** - Reed-Solomon поверх KCP.
- **`raw_frame_size` / `raw_fps` / `raw_copies` / `raw_gop`** - как формируется VP8-поток у raw-транспорта.
- **`squares_*`** - размер сетки, размер клетки, бит на клетку, fps и битрейт у squares-транспорта.

## TUN

TUN работает на Linux и на Android (через `VpnService` fd).

На Linux при старте TUN:
1. Читает текущий default route.
2. Резолвит все bypass-хосты транспорта (плюс IP-пиры из WebRTC для Telemost) и добавляет для них отдельные маршруты через старый шлюз - чтобы трафик к самому транспорту не заворачивался в туннель.
3. Поднимает `/dev/net/tun`, настраивает адрес, дефолтный маршрут, стартует `tun2socks` в режиме `mux://`.
4. При остановке всё это аккуратно откатывает.

Нужен root, потому что меняем маршруты ядра.

## Архитектура

```
cmd/                       - cli
mobile/                    - gomobile entrypoint (Android)
internal/app/              - реестр транспортов, Settings, Runtime
internal/multiplexing/     - логические стримы поверх одного канала
internal/socks5/           - SOCKS5-сервер и проксирование стримов в TCP
internal/runtime/          - TUN (Linux), VPN fd (Android), tun2socks, muxproxy
internal/runtime/muxproxy  - Proxy для tun2socks поверх StreamMultiplexer
internal/core/framing/     - сетка, палитра, VP8-кодек (ffmpeg на Linux, MediaCodec на Android)
internal/core/reliability/ - ReliableSession и KCP
internal/core/security/    - XChaCha20, padding, jitter, FEC
internal/provider/telemost - WebRTC-сигналинг и VP8-транспорт Телемоста
internal/transport/        - Pipe, Transport, декораторы, конкретные транспорты
```

---

## Транспорт: Yandex Board

### Где взять хэш

1. Откройте [boards.yandex.ru](https://boards.yandex.ru).
2. Создайте новую доску. **Используйте новую, чистую доску.**
3. **Поделиться** > **Редактирование для всех** > **Включить гостевой доступ** > **Права гостей: редактирование**.
4. Скопируйте хэш из ссылки. Она выглядит так: `https://boards.yandex.ru/whiteboard/?hash=YOUR_HASH`

Данные ходят base64-строками в объектах на доске. Surface-протокол: WebSocket + socket.io, поверх - ReliableSession.

---

## Транспорт: Yandex Telemost

### Где взять ссылку

1. Откройте [telemost.yandex.ru](https://telemost.yandex.ru).
2. Создайте новую конференцию.
3. Скопируйте ссылку из адресной строки. Она выглядит так: `https://telemost.yandex.ru/j/123456789012345`.

Клиент и сервер должны быть в **одной и той же** конференции. Каждый заходит в неё как отдельный участник.

### raw VP8

Публикует VP8-трек в SFU Телемоста, в кадры кладёт куски полезной нагрузки (с фейковым P-frame заголовком). Поверх - KCP. Размер кадра регулируется `raw_frame_size`, частота - `raw_fps`, дубли - `raw_copies`.

### squares

Рисует в кадре сетку клеток, каждая клетка кодирует N бит. Рисуется поверх VP8, который тоже кодирует кадр, но даже если VP8 что-то смазал - биты из соседних клеток частично выживают. Поверх - KCP и опционально FEC.

---

## Статус

Автор планинирует запилить приложуху на андроид. Мб какие-нибудь другие весёлые транспорты сделать.

## О проекте

Изначально проект сделан на Python, но Python для такого проекта - ещё то извращение, так что оно переделано и дальше пишется на го

Я вдохновился [olcrtc](https://github.com/openlibrecommunity/olcrtc) и [OpenFlux](https://github.com/p1neapplexpress/OpenFlux), и захотел наколхозить что-то своё