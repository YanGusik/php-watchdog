# php-watchdog

A standalone daemon that monitors PHP worker processes from the outside via `/proc`. No code changes required in your application. Optionally integrates with any framework via a Unix socket.

## The Problem

Laravel/Symfony workers run for a long time. When a process is killed by the OOM killer or crashes — you only see `Killed` in the system log. No information about:

- which job was running at the time of death
- how memory grew before the kill
- what data (payload) the job was processing

`memory_get_usage()` is useless here — by the time the process is killed it's too late, and it shows what PHP thinks about itself, not the real RSS. The OOM killer looks at RSS via `/proc/PID/status` — these are different numbers, especially with leaks in C extensions (curl, PDO).

## How It Works

The daemon watches PHP worker processes **from the outside** via `/proc`. It does not touch your application and does not slow down job execution.

```
/proc/PID/status  →  RSS snapshots  →  ring buffer  →  anomaly detection  →  report
```

Optionally, a framework module (Laravel, Symfony, etc.) sends job context to the daemon via a Unix socket. The daemon stores this context and includes it in the report.

---

## Installation

### Requirements

- Linux (uses `/proc`)
- Go 1.22+ (to build from source)

### Build from source

```bash
git clone https://github.com/yangusik/php-watchdog
cd php-watchdog

go build -o watchdog ./cmd/watchdog/
go build -o rss-check ./cmd/rss-check/
```

### Install binary

```bash
cp watchdog /usr/local/bin/watchdog
chmod +x /usr/local/bin/watchdog
```

---

## Configuration

Copy the example config and edit it:

```bash
cp watchdog.yml.example watchdog.yml
```

```yaml
interval: 5        # seconds between RSS snapshots
ring_buffer: 60    # snapshots kept per process in memory

socket: /var/run/watchdog.sock  # Unix socket for framework modules (optional)

watchers:
  - name: "queue-workers"
    mask: "queue:work"           # substring or glob match against /proc/PID/cmdline
    thresholds:
      rss_absolute_mb: 500       # kill if RSS exceeds 500 MB
      growth_snapshots: 10       # kill if RSS grows for 10 consecutive snapshots
      pool_rss_total_mb: 4096    # optional: kill if total RSS of all matched processes exceeds 4 GB
      pool_kill_strategy: "heaviest"  # required with pool_rss_total_mb: "heaviest" or "all"
    on_anomaly:
      kill: true
      dump_path: /var/log/watchdog/
      webhook: ""   # optional: HTTP POST with JSON report
      exec: ""      # optional: path to script, context passed via env vars
```

### Mask syntax

The `mask` field matches against the full command line of the process (`/proc/PID/cmdline`).

| Mask | Matches |
|------|---------|
| `queue:work` | any process containing `queue:work` |
| `horizon:work` | any process containing `horizon:work` |
| `horizon:work*--queue=critical` | horizon workers on the `critical` queue |
| `horizon:work*supervisor-ai-*` | horizon workers under any `supervisor-ai-*` supervisor |

### Pool kill strategies

When `pool_rss_total_mb` is triggered:

- `heaviest` — kills the single process with the highest RSS, re-checks on the next tick. Repeats until the pool is back under the limit.
- `all` — kills all matched processes immediately.

### on_anomaly exec

When `exec` is set, the script is called with context via environment variables:

| Variable | Value |
|----------|-------|
| `WATCHDOG_PID` | process ID |
| `WATCHDOG_RSS_MB` | current RSS in MB |
| `WATCHDOG_REASON` | anomaly reason string |
| `WATCHDOG_DUMP_FILE` | path to the written report file |
| `WATCHDOG_STARTED_AT` | ISO 8601 timestamp (if framework module connected) |
| `WATCHDOG_META_*` | any key from `meta` sent by the framework module |

Example — if Laravel module sent `meta.job = App\Jobs\SendEmail`, the script receives `WATCHDOG_META_JOB=App\Jobs\SendEmail`.

### on_anomaly webhook

When `webhook` is set, a `POST` request is sent with a JSON body:

```json
{
  "pid": 1234,
  "reason": "RSS threshold exceeded",
  "detail": "RSS 512.3 MB exceeds threshold 500.0 MB",
  "generated_at": "2026-03-13T12:00:00Z",
  "rss": {
    "current_mb": 512.3,
    "start_mb": 80.1,
    "growth_mb": 432.2
  },
  "started_at": "2026-03-13T11:59:13Z",
  "meta": {
    "job": "App\\Jobs\\ProcessCampaign",
    "queue": "default",
    "campaign_id": 1234
  },
  "dump_file": "/var/log/watchdog/watchdog-1234-20260313-120000.txt"
}
```

---

## Running

```bash
# foreground
./watchdog --config=/etc/watchdog.yml

# with systemd
```

```ini
# /etc/systemd/system/watchdog.service
[Unit]
Description=php-watchdog
After=network.target

[Service]
ExecStart=/usr/local/bin/watchdog --config=/etc/watchdog.yml
Restart=always
User=www-data

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable watchdog
systemctl start watchdog
```

---

## Report format

When an anomaly is detected or a process disappears, a report is written to `dump_path`:

```
═══════════════════════════════════════════════════
WATCHDOG REPORT — PID 1234
═══════════════════════════════════════════════════
Time:    2026-03-12 14:32:48
Reason:  RSS threshold exceeded
Detail:  RSS 512.3 MB exceeds threshold 500.0 MB

Started: 14:32:01 (ran 47s before report)

Context:
  job: App\Jobs\ProcessCampaign
  queue: default
  campaign_id: 1234

RSS at report: 512.3 MB
RSS at start:  80.1 MB
Growth:        +432.2 MB over 47s

RSS Timeline (last 10 snapshots):
  14:32:01  80.1 MB
  14:32:06  130.2 MB  ▲ +50.1 MB
  14:32:11  210.5 MB  ▲ +80.3 MB
  ...
  14:32:46  512.3 MB  ▲ +25.1 MB  ← REPORT POINT
═══════════════════════════════════════════════════
```

---

## rss-check utility

A helper tool to inspect RSS of running processes:

```bash
# all processes
./rss-check

# filtered
./rss-check --filter=php
./rss-check --filter=queue:work
./rss-check --filter=horizon
```

---

## Framework integration (optional)

The daemon listens on a Unix socket and accepts JSON from any module. The module sends context before processing starts — the daemon stores it and includes it in the report if an anomaly occurs.

### Socket protocol

Send a single JSON object per connection:

```json
{
  "pid": 1234,
  "started_at": "2026-03-13T12:00:00Z",
  "meta": {
    "anything": "you want"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `pid` | int | yes | PID of the worker process |
| `started_at` | RFC3339 string | yes | when the current job/task started |
| `meta` | object | no | any key-value data to include in the report |

Rules:
- One JSON object per connection, connection closes immediately after write
- Fire-and-forget — do not wait for a response
- Max recommended payload: 2 KB (atomic write guarantee on Linux)
- The daemon overwrites context on each new message for the same PID

### Writing a module

A module is any code that:
1. Opens a Unix socket connection to the configured `socket` path
2. Writes the JSON object
3. Closes the connection

#### PHP example (minimal)

```php
function watchdog_send(string $socketPath, int $pid, array $meta = []): void
{
    if (!file_exists($socketPath)) {
        return;
    }

    $socket = @socket_create(AF_UNIX, SOCK_STREAM, 0);
    if (!$socket) {
        return;
    }

    if (@socket_connect($socket, $socketPath)) {
        socket_write($socket, json_encode([
            'pid'        => $pid,
            'started_at' => (new DateTimeImmutable())->format(DateTimeInterface::RFC3339),
            'meta'       => $meta,
        ]));
    }

    socket_close($socket);
}
```

#### Laravel module example

```php
// app/Jobs/Middleware/WatchdogMiddleware.php

namespace App\Jobs\Middleware;

use Closure;

class WatchdogMiddleware
{
    public function __construct(
        private string $socketPath = '/var/run/watchdog.sock'
    ) {}

    public function handle(object $job, Closure $next): void
    {
        $this->send([
            'pid'        => getmypid(),
            'started_at' => now()->toISOString(),
            'meta'       => array_merge(
                [
                    'job'   => get_class($job),
                    'queue' => $job->queue ?? 'default',
                ],
                method_exists($job, 'watchdogPayload') ? $job->watchdogPayload() : []
            ),
        ]);

        $next($job);
    }

    private function send(array $data): void
    {
        if (!file_exists($this->socketPath)) {
            return;
        }

        $socket = @socket_create(AF_UNIX, SOCK_STREAM, 0);
        if (!$socket) {
            return;
        }

        if (@socket_connect($socket, $this->socketPath)) {
            socket_write($socket, json_encode($data));
        }

        socket_close($socket);
    }
}
```

Add to your job:

```php
class MyJob implements ShouldQueue
{
    public function middleware(): array
    {
        return [new WatchdogMiddleware()];
    }

    // Optional: expose payload for the report
    public function watchdogPayload(): array
    {
        return [
            'campaign_id' => $this->campaignId,
        ];
    }
}
```

#### Symfony module example

```php
// src/Messenger/Middleware/WatchdogMiddleware.php

namespace App\Messenger\Middleware;

use Symfony\Component\Messenger\Envelope;
use Symfony\Component\Messenger\Middleware\MiddlewareInterface;
use Symfony\Component\Messenger\Middleware\StackInterface;

class WatchdogMiddleware implements MiddlewareInterface
{
    public function __construct(
        private string $socketPath = '/var/run/watchdog.sock'
    ) {}

    public function handle(Envelope $envelope, StackInterface $stack): Envelope
    {
        $this->send([
            'pid'        => getmypid(),
            'started_at' => (new \DateTimeImmutable())->format(\DateTimeInterface::RFC3339),
            'meta'       => [
                'message' => get_class($envelope->getMessage()),
            ],
        ]);

        return $stack->next()->handle($envelope, $stack);
    }

    private function send(array $data): void
    {
        if (!file_exists($this->socketPath)) {
            return;
        }

        $socket = @socket_create(AF_UNIX, SOCK_STREAM, 0);
        if (!$socket) {
            return;
        }

        if (@socket_connect($socket, $this->socketPath)) {
            socket_write($socket, json_encode($data));
        }

        socket_close($socket);
    }
}
```

Register in `config/packages/messenger.yaml`:

```yaml
framework:
  messenger:
    buses:
      messenger.bus.default:
        middleware:
          - App\Messenger\Middleware\WatchdogMiddleware
```

### Testing the socket

Send a test message manually:

```bash
php -r "
\$s = socket_create(AF_UNIX, SOCK_STREAM, 0);
socket_connect(\$s, '/var/run/watchdog.sock');
socket_write(\$s, json_encode([
    'pid'        => (int) shell_exec('pgrep -f queue:work | head -1'),
    'started_at' => date('c'),
    'meta'       => ['job' => 'TestJob', 'queue' => 'default'],
]));
socket_close(\$s);
echo 'sent' . PHP_EOL;
"
```

Or with `socat`:

```bash
echo '{"pid":1234,"started_at":"2026-03-13T12:00:00Z","meta":{"job":"TestJob"}}' \
  | socat - UNIX-CONNECT:/var/run/watchdog.sock
```

---

## Docker

The Linux kernel maintains a **single process table**. Containers are just namespaced views of the same processes. This means if you mount the host's `/proc` into the watchdog container — it sees processes from **all containers** on the host. This is the same approach used by cAdvisor and Prometheus node-exporter.

### Sidecar with host /proc mount (recommended)

```yaml
# docker-compose.yml
services:
  horizon:
    image: your-app-image
    command: php artisan horizon
    volumes:
      - watchdog-socket:/var/run/watchdog  # shared socket directory

  watchdog:
    image: yangusik/php-watchdog:latest
    volumes:
      - /proc:/proc:ro                               # host /proc — sees ALL container processes
      - ./watchdog.yml:/etc/watchdog/watchdog.yml:ro
      - watchdog-socket:/var/run/watchdog            # shared socket directory
      - watchdog-reports:/var/log/watchdog
    restart: unless-stopped
    user: root

volumes:
  watchdog-socket:
  watchdog-reports:
```

One watchdog instance monitors all containers on the host — no need to couple it to a specific container.

> **Note for WSL2 + Docker Desktop:** Docker containers run inside a separate VM, so the WSL2 host `/proc` does not contain container processes. Use `pid: "service:app"` instead (see Option 2 below).

`watchdog.yml` — point socket to the shared volume:

```yaml
socket: /var/run/watchdog/watchdog.sock

watchers:
  - name: "horizon-workers"
    mask: "horizon:work"
    thresholds:
      rss_absolute_mb: 500
      growth_snapshots: 10
    on_anomaly:
      kill: true
      dump_path: /var/log/watchdog/
```

The Laravel/Symfony module should point to the shared socket path:

```php
new WatchdogMiddleware('/var/run/watchdog/watchdog.sock')
```

### Option 2 — Inside the same container

Add watchdog to your existing container and run it alongside the PHP process using supervisord:

```ini
# supervisord.conf
[program:horizon]
command=php artisan horizon
autostart=true
autorestart=true

[program:watchdog]
command=/usr/local/bin/watchdog --config=/etc/watchdog/watchdog.yml
autostart=true
autorestart=true
```

### Build the Docker image

```bash
docker build -t php-watchdog .

# or with docker compose
docker compose build watchdog
```

### Note on permissions

`/proc/PID/status` and `/proc/PID/cmdline` are readable by **any user** on Linux — no root required for monitoring.

The only operation that requires elevated privileges is **killing processes** (`kill: true`). Sending `SIGKILL` to another process requires either root or matching UID.

The recommended setup is to run watchdog as the **same user as your PHP workers**:

```yaml
user: "1000:1000"  # match the UID of PHP workers
```

This way:
- The Unix socket is created with the right ownership — PHP workers can write to it
- Killing workers works because UIDs match
- No root needed

If watchdog runs as a different user than PHP workers (e.g. root), the Unix socket is automatically created with `0666` permissions so any user can connect to it.

To find the UID of your PHP workers:
```bash
docker exec <container> id www-data
docker exec <container> id sail
```

If `kill: false` — watchdog works as any user with no special permissions required.

---

## Architecture

```
php-watchdog/
├── cmd/
│   ├── watchdog/        # daemon entry point
│   └── rss-check/       # standalone RSS inspection utility
├── internal/
│   ├── proc/            # /proc reader — isolated OS layer
│   ├── ring/            # fixed-size ring buffer for snapshots
│   ├── watcher/         # main loop — orchestrates everything
│   ├── detector/        # anomaly detection strategies
│   ├── report/          # report formatting and file output
│   ├── socket/          # Unix socket server + context store
│   └── webhook/         # HTTP webhook sender
├── config/              # config struct + YAML parsing
├── watchdog.yml.example
└── README.md
```

### Detectors

| Detector | Trigger |
|----------|---------|
| `ThresholdDetector` | RSS of a single process exceeds `rss_absolute_mb` |
| `TrendDetector` | RSS grows for `growth_snapshots` consecutive snapshots |
| `PoolDetector` | Total RSS of all matched processes exceeds `pool_rss_total_mb` |

---

## What this tool does NOT do

- **Does not find the leak location** — that requires profiling tools (Blackfire, Xdebug, Valgrind). Watchdog tells you *when* it started, *how fast* it grows, and *which job* with what data. The rest is your job.
- **Is not a replacement for Horizon** — Horizon shows queue statistics. Watchdog watches processes and memory.
- **Does not work on macOS/Windows** — depends on Linux `/proc`.
