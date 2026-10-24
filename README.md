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
Приложения -> (SOCKS5) -> shampoore-client -> кастомный транспорт -> shampoore-server -> Интернет
Приложения -> (TUN) -> shampoore-client -> кастомный транспорт -> shampoore-server -> Интернет
```

- **Клиент** работает в одном из двух режимов (флаг `-m`):
  - `socks5` - локальный SOCKS5-прокси, маршруты не трогаем;
  - `tun` - TUN-интерфейс, весь системный трафик через туннель.
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

Нужны: Go >= 1.23, Android NDK, gomobile. Дальше всё через скрипт:

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
gomobile init

export ANDROID_NDK_HOME=/path/to/ndk
./build-aar.sh
```

На выходе появятся `shampoore.aar` и `shampoore-sources.jar`.

## Формат конфига

Конфиг - JSON, единый для всех транспортов:

```json
{
  "name": "название",
  "type": "yandex",
  "transport": { "hash": "..." },
  "encryption_password": "VERY HARD PASSWORD",
  "advanced": { "tun_mtu": 1400 }
}
```

- **`type`** - kind транспорта (`yandex`, `yandex_telemost_raw`, `yandex_telemost_squares`).
- **`transport`** - транспорт-специфичный конфиг.
- **`encryption_password`** - пароль для XChaCha20-Poly1305. Должен совпадать на клиенте и сервере.
- **`advanced`** - плоский map с оверрайдами дефолтов. Попадает в URL как `a.<key>=<value>`. Что не задано - берётся из дефолтов транспорта.

Режим (`socks5`/`tun`) в конфиге не хранится - только флаг CLI `-m` (или `VpnService` на мобилке).

## Примеры конфигов

### Yandex Board

```json
{
  "name": "yandex-board",
  "type": "yandex",
  "transport": {
    "hash": "yandexboard_hash"
  },
  "encryption_password": "VERY HARD PASSWORD"
}
```

### Yandex Telemost raw

```json
{
  "name": "telemost-raw",
  "type": "yandex_telemost_raw",
  "transport": {
    "conference_id": "hash",
    "display_name": "guest"
  },
  "encryption_password": "VERY HARD PASSWORD"
}
```

`conference_id` - это только хэш из ссылки `https://telemost.yandex.ru/j/<хэш>`.

### Yandex Telemost squares

```json
{
  "name": "telemost-squares",
  "type": "yandex_telemost_squares",
  "transport": {
    "conference_id": "hash",
    "display_name": "guest"
  },
  "encryption_password": "VERY HARD PASSWORD"
}
```

## Запуск

**На сервере:**

```bash
./shampoore-server -c config.json
```

Серверу режим не нужен, он всегда один и тот же.

**На клиенте:**

```bash
# SOCKS5 - без sudo
./shampoore-client -c config.json -m socks5

# TUN - нужен root, потому что меняем маршруты ядра
sudo ./shampoore-client -c config.json -m tun
```

Флаги клиента:

| Флаг | Что делает |
|------|------------|
| `-c` | путь к конфигу или `shampoore://` URL |
| `-m` | `socks5` (дефолт) или `tun` |

Проверка:

```bash
# SOCKS5
curl -4 --socks5-hostname 127.0.0.1:1080 https://ifconfig.me

# TUN
curl -4 https://ifconfig.me
```

## Конфиг через URL

Конфиг кодируется в одну строку по аналогии с `vless://` и `ss://`.

В URL попадает:

- `type` - kind транспорта (обязательно);
- параметры транспорта (`hash`, `conf`, `name`);
- `pw` - пароль;
- `a.<key>=<value>` - только те `advanced`-ключи, которые юзер явно выставил;
- `#<name>` - имя конфига.

Генерация ссылки из json:

```bash
./shampoore-server -c config.json -encode
```

Клиент запускается прямо с URL:

```bash
./shampoore-client -c "shampoore://?type=yandex&hash=...&pw=..." -m socks5
./shampoore-client -c "shampoore://?type=yandex_telemost_raw&conf=...&pw=..." -m tun
```

```bash
sudo ./shampoore-client -c "shampoore://?type=yandex_telemost_raw&conf=...&pw=...&a.kcp_window=1024&a.tun_mtu=1400" -m tun
```

## Шифрование

Трафик между клиентом и сервером шифруется симметричным **XChaCha20-Poly1305**. Ключ выводится из пароля через HKDF-SHA256. Пароль задаётся в `encryption_password` и должен совпадать на обеих сторонах - клиент и сервер с разными паролями не установят соединение.

Для Telemost-транспортов дополнительно доступен **FEC** (Reed-Solomon) поверх KCP.

Пароль:

```bash
openssl rand -base64 48
```

## Advanced

Всё продвинутое живёт в секции `advanced`. Если не указывать, берутся дефолты. Указывайте, только если знаете зачем.

### Общие

```json
{
  "advanced": {
    "socks5_listen": "127.0.0.1:1080",
    "tun_name": "shampoore0",
    "tun_mtu": 1500,
    "tun_inet4": "172.19.0.1/30",
    "tun_auto_route": true,
    "tun_dns": "1.1.1.1:53,8.8.8.8:53",
    "tun_bypass": "example.com,another.host"
  }
}
```

- **`socks5_listen`** - адрес SOCKS5 в `-m socks5`. В `-m tun` игнорится.
- **`tun_name`** - имя TUN-интерфейса.
- **`tun_mtu`** - MTU. Только Linux CLI; на Android задаётся в `VpnService.Builder`.
- **`tun_inet4`** - адрес TUN с маской.
- **`tun_auto_route`** - менять ли дефолтный маршрут. `false` - TUN поднимается, роутинг не трогаем.
- **`tun_dns`** - DNS для резолва bypass-хостов, через запятую.
- **`tun_bypass`** - доп. хосты/IP мимо туннеля, через запятую.

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
- **`mux_flush_threshold`** - сколько байт накопить, чтобы отправить не дожидаясь `mux_flush_ms`.
- **`reliable_window`** - сколько пакетов в полёте одновременно.
- **`reliable_chunk`** - размер чанка. 0 - автоматически.
- **`padding_enabled`** - выравнивание размера сообщений.
- **`padding_target`** - до какого размера выравнивать, в байтах.
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

### Yandex Telemost squares

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

- **`kcp_conv`** - conv id для KCP. 0 - выводится из ID конференции, клиент и сервер сойдутся автоматически.
- **`kcp_mtu`** - максимальный размер пакета KCP. Для squares сильно ниже (390), потому что кадр несёт мало данных.
- **`kcp_window`, `kcp_nodelay`, `kcp_interval`, `kcp_resend`, `kcp_nc`** - стандартные настройки KCP.
- **`fec_enabled` / `fec_data_shards` / `fec_parity_shards`** - Reed-Solomon поверх KCP.
- **`raw_*`** - как формируется VP8-поток у raw-транспорта.
- **`squares_*`** - сетка, размер клетки, бит на клетку, fps и битрейт у squares.

## TUN

TUN работает на Linux и на Android (через `VpnService` fd).

На Linux при старте TUN (`-m tun`):

1. Читает текущий default route.
2. Резолвит все bypass-хосты транспорта (плюс IP-пиры из WebRTC для Telemost) и добавляет для них отдельные маршруты через старый шлюз - чтобы трафик к самому транспорту не заворачивался в туннель.
3. Поднимает `/dev/net/tun`, настраивает адрес, дефолтный маршрут, стартует `tun2socks` в режиме `mux://`.
4. При остановке всё откатывает.

Нужен root, потому что меняем маршруты ядра.

На Android всё то же самое, но:

- host-приложение (Kotlin) создаёт `VpnService` и передаёт `fd` в Go через `Mobile.startClientVPN(source, fd, mtu)`;
- параметры TUN задаются в `VpnService.Builder`, а не в конфиге;
- `mode` не используется - приложение зовёт либо `startClient` (SOCKS5), либо `startClientVPN` (TUN).

## API для мобильного

Пакет `mobile` через gomobile экспортирует:

```kotlin
// SOCKS5-only. Слушает на advanced.socks5_listen.
Mobile.startClient(source: String): String

// TUN-only. fd и mtu готовит host-приложение.
Mobile.startClientVPN(source: String, fd: Int, mtu: Int): String

// Остановка любого режима (внутри таймаут 7с).
Mobile.stopClient()

Mobile.isRunning(): Boolean
```

`source` - путь к JSON-файлу или `shampoore://`-URL.

## Архитектура

```
cmd/                       - cli (client, server)
mobile/                    - gomobile entrypoint (Android)
internal/app/              - реестр транспортов, Settings, Runtime, Values, URL
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
4. Скопируйте хэш из ссылки: `https://boards.yandex.ru/whiteboard/?hash=YOUR_HASH`.

В `transport.hash`:

```json
{
  "type": "yandex",
  "transport": { "hash": "YOUR_HASH" },
  "encryption_password": "..."
}
```

Данные ходят base64-строками в объектах на доске. Surface-протокол: WebSocket + socket.io, поверх - ReliableSession.

---

## Транспорт: Yandex Telemost

### Где взять ID

1. Откройте [telemost.yandex.ru](https://telemost.yandex.ru).
2. Создайте новую конференцию.
3. Скопируйте ссылку: `https://telemost.yandex.ru/j/123456789012345`.
4. В конфиг идёт только последний сегмент (`123456789012345`) как `conference_id`.

Клиент и сервер должны быть в **одной и той же** конференции. Каждый заходит как отдельный участник.

```json
{
  "type": "yandex_telemost_raw",
  "transport": {
    "conference_id": "your_id",
    "display_name": "guest"
  },
  "encryption_password": "..."
}
```

### raw VP8

Публикует VP8-трек в SFU Телемоста, в кадры кладёт куски полезной нагрузки (с фейковым P-frame заголовком). Поверх - KCP. Размер кадра регулируется `raw_frame_size`, частота - `raw_fps`, дубли - `raw_copies`.

### squares

Рисует в кадре сетку клеток, каждая клетка кодирует N бит. Рисуется поверх VP8, который тоже кодирует кадр, но даже если VP8 что-то смазал - биты из соседних клеток частично выживают. Поверх - KCP и опционально FEC.

---

## Статус

Автор планирует запилить приложуху на андроид. Мб какие-нибудь другие весёлые транспорты сделать.

## О проекте

Изначально проект сделан на Python, но Python для такого проекта - ещё то извращение, так что оно переделано и дальше пишется на го.

Я вдохновился [olcrtc](https://github.com/openlibrecommunity/olcrtc) и [OpenFlux](https://github.com/p1neapplexpress/OpenFlux), и захотел наколхозить что-то своё.