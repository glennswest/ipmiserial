# Console Server

A Go-based console server that captures Serial-over-LAN (SOL) output from Supermicro IPMI servers, logs to files, and provides a web UI with per-server tabs showing live and historical output.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Console Server (Go)                       │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │  Discovery   │  │  SOL Manager │  │   Web Server     │   │
│  │  (subnet     │  │  (per-server │  │   (tabs + SSE    │   │
│  │   scanner)   │  │   goroutine) │  │    streaming)    │   │
│  └──────────────┘  └──────────────┘  └──────────────────┘   │
│         │                 │                   │              │
│         ▼                 ▼                   ▼              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              Server State Manager                     │   │
│  │  - Tracks power state, reboot detection               │   │
│  │  - Manages log file rotation                          │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
          │
          ▼
┌─────────────────┐
│  /data/logs/    │
│  └─ <hostname>/ │
│     └─ YYYY-MM- │
│        DD_HH-   │
│        MM-SS.log│
└─────────────────┘
```

## Components

### 1. Discovery Service
- Scans 192.168.11.0/24 subnet for IPMI (UDP 623)
- Uses DNS reverse lookup to get server names
- Periodic rescan (configurable, default 5 min)
- Filters out non-responding/non-IPMI hosts

### 2. SOL Manager
- Uses ipmitool CLI for SOL connections (most reliable with Supermicro)
- One goroutine per server managing the SOL session
- Reconnects on disconnect with backoff
- Pipes output to log file and broadcast channel

### 3. Reboot Detection
- SOL parsing: Regex for BIOS strings like "POST", "BIOS", "Booting"
- IPMI polling: Check chassis power status every 30s
- On reboot detected: close current log, start new timestamped log

### 4. Log Management
- Directory structure: `/data/logs/<hostname>/`
- Filename format: `YYYY-MM-DD_HH-MM-SS.log`
- Current log symlinked as `current.log`
- Retention policy configurable (default keep 30 days)

### 5. Web UI
- Single page app with Bootstrap/vanilla JS
- Server-Sent Events (SSE) for live streaming
- Tab per server showing:
  - Live tab: Real-time console output (SSE stream)
  - History tab: List of past log files with viewer

## Configuration

```yaml
# config.yaml
ipmi:
  username: admin
  password: ${IPMI_PASSWORD}  # from env

discovery:
  subnet: "192.168.11.0/24"
  interval: 5m

reboot_detection:
  sol_patterns:
    - "POST"
    - "BIOS"
    - "Booting"
  chassis_poll_interval: 30s

logs:
  path: /data/logs
  retention_days: 30

server:
  port: 8080
```

## File Structure

```
console_server/
├── main.go                 # Entry point
├── config/
│   └── config.go           # Config loading
├── discovery/
│   └── scanner.go          # Subnet scanning, DNS lookup
├── sol/
│   ├── manager.go          # SOL session management
│   └── reboot.go           # Reboot detection logic
├── logs/
│   └── writer.go           # Log file management
├── server/
│   ├── server.go           # HTTP server setup
│   ├── handlers.go         # API handlers
│   └── sse.go              # SSE streaming
├── web/
│   ├── index.html          # Main page
│   ├── app.js              # Frontend logic
│   └── style.css           # Styles
├── config.yaml.example
└── go.mod
```

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /` | Web UI |
| `GET /api/servers` | List discovered servers with status |
| `GET /api/servers/{name}/stream` | SSE stream of live output |
| `GET /api/servers/{name}/logs` | List log files for server |
| `GET /api/servers/{name}/logs/{filename}` | Get log file content |
| `GET /api/servers/{name}/status` | Server power/connection status |

## Deployment

Target server: `console.g11.lo`

### Initial Install

```bash
./install.sh
```

This will:
- Build the Go binary
- Copy binary and config to console.g11.lo
- Set up systemd service
- Create log directories
- Start the service

### Update

```bash
./update.sh
```

This will:
- Build the Go binary
- Copy new binary to console.g11.lo
- Restart the service

## Dependencies

- Go 1.21+
- ipmitool (must be installed on console.g11.lo)

### Go Modules
- `github.com/spf13/viper` - Config
- `github.com/gorilla/mux` - Router
- `github.com/sirupsen/logrus` - Logging

## Implementation Order

1. Project setup (go.mod, config loading)
2. Discovery service (subnet scan + DNS)
3. SOL manager (ipmitool wrapper, reconnect logic)
4. Log writer (file rotation, reboot detection)
5. Web server + API endpoints
6. SSE streaming
7. Web UI (HTML/JS)
8. Testing and deployment

## Verification

1. Build and run locally: `go build && ./console_server`
2. Check discovery finds servers: `curl localhost:8080/api/servers`
3. Check SOL streaming: `curl localhost:8080/api/servers/{name}/stream`
4. Verify web UI loads and shows tabs
5. Test reboot detection by rebooting a server
