# Console Server

A high-performance, native Go implementation of Serial-Over-LAN (SOL) console aggregation for bare metal server management.

## Features

- **Native Go SOL Implementation**: Pure Go IPMI v2.0/RMCP+ protocol stack - no external dependencies like `ipmitool`
- **Scalable Architecture**: Handles dozens of concurrent SOL connections with minimal resource usage
- **Live Console Streaming**: Real-time SSE-based console output in web browser
- **Boot Analytics**: Automatic detection of reboots, boot timing, and OS identification
- **Log Management**: Automatic log rotation, retention policies, and searchable history
- **Scratch Container**: Minimal container image (~10MB) with just the static Go binary
- **Auto-Discovery**: Integrates with Netman for automatic server discovery via IPMI network scanning

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Console Server                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐      │
│  │   Discovery  │    │  SOL Manager │    │  HTTP Server │      │
│  │   Scanner    │───▶│              │◀───│   (Gorilla)  │      │
│  └──────────────┘    └──────────────┘    └──────────────┘      │
│         │                   │                    │               │
│         ▼                   ▼                    ▼               │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐      │
│  │    Netman    │    │   go-sol     │    │   Web UI     │      │
│  │     API      │    │   Library    │    │  (xterm.js)  │      │
│  └──────────────┘    └──────────────┘    └──────────────┘      │
│                            │                                     │
│                            ▼                                     │
│                     ┌──────────────┐                            │
│                     │  Analytics   │                            │
│                     │   Engine     │                            │
│                     └──────────────┘                            │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ IPMI/RMCP+ (UDP 623)
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Bare Metal Servers                          │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐           │
│  │ Server1 │  │ Server2 │  │ Server3 │  │   ...   │           │
│  │  BMC    │  │  BMC    │  │  BMC    │  │         │           │
│  └─────────┘  └─────────┘  └─────────┘  └─────────┘           │
└─────────────────────────────────────────────────────────────────┘
```

### Component Overview

| Component | Description |
|-----------|-------------|
| **Discovery Scanner** | Polls Netman API for IPMI hosts, filters by IP range, manages server inventory |
| **SOL Manager** | Manages concurrent SOL sessions, handles reconnection with exponential backoff |
| **go-sol Library** | Native Go IPMI v2.0/RMCP+ implementation with queue-based buffering for bursty traffic |
| **Analytics Engine** | Detects BIOS boot patterns, tracks boot timing, identifies OS/images |
| **HTTP Server** | RESTful API + SSE streaming + static file serving for web UI |
| **Log Writer** | ANSI-cleaned log storage with rotation and retention management |

### Data Flow

1. **Discovery**: Scanner queries Netman for hosts on IPMI network (192.168.11.0/24)
2. **Connection**: SOL Manager establishes IPMI v2.0 authenticated sessions to each BMC
3. **Data Capture**: go-sol library receives console output via UDP, buffers with 10k-entry queue
4. **Processing**: Data is written to logs, analyzed for boot patterns, and broadcast to SSE subscribers
5. **Display**: Web UI receives SSE events and renders in xterm.js terminal emulator

## File Structure

```
console_server/
├── main.go                 # Entry point, component wiring
├── config/
│   └── config.go           # YAML config loading
├── discovery/
│   └── scanner.go          # Netman integration, server tracking
├── sol/
│   ├── manager.go          # SOL session lifecycle management
│   ├── reboot.go           # Reboot pattern detection
│   └── analytics.go        # Boot analytics engine
├── logs/
│   └── writer.go           # Log file management, ANSI cleaning
├── server/
│   ├── server.go           # HTTP server, routing
│   ├── handlers.go         # REST API handlers
│   ├── sse.go              # Server-Sent Events streaming
│   └── web/                # Embedded static files
│       ├── index.html
│       ├── app.js
│       └── style.css
├── config.yaml.example
├── Dockerfile
├── build.sh
├── deploy.sh
└── go.mod
```

## Installation

### Prerequisites

- Go 1.21+ (for building)
- Podman (for container image creation)
- Target platform: ARM64 (MikroTik RouterOS container)

### Building

```bash
# Build the container image
./build.sh

# Deploy to MikroTik router
./deploy.sh
```

### Manual Build

```bash
# Build static binary for ARM64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o console-server .

# Create container with podman
podman build -t console-server:latest .
podman save console-server:latest -o console-server.tar
```

## Security

**The credentials shown in examples are placeholders only.** Always use strong, unique credentials for your BMC/IPMI accounts. Never commit real credentials to source control. Store `config.yaml` outside of version control or use environment variables.

## Configuration

Create `config.yaml`:

```yaml
ipmi:
  username: ADMIN    # Example only - change to your credentials
  password: ADMIN    # Example only - change to your credentials

discovery:
  netman_url: "http://network.g10.lo"
  ip_range_min: 10
  ip_range_max: 199

logs:
  path: /var/lib/data/logs
  retention_days: 30

server:
  port: 80

reboot_detection:
  sol_patterns:
    - "POST"
    - "BIOS"
    - "Booting"

# Optional: static server definitions (in addition to auto-discovery)
servers:
  - name: server1
    host: 192.168.11.10
    macs:
      - "00:25:90:xx:xx:xx"
```

## API Reference

### Servers

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/servers` | GET | List all servers with connection status |
| `/api/servers/{name}/status` | GET | Get detailed status for a server |
| `/api/servers/{name}/stream` | GET | SSE stream of live console output |
| `/api/refresh` | POST | Trigger immediate Netman refresh |

### Logs

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/servers/{name}/logs` | GET | List log files for a server |
| `/api/servers/{name}/logs/{file}` | GET | Get log file content |
| `/api/servers/{name}/logs/{file}/info` | GET | Get log file metadata |
| `/api/servers/{name}/logs/clear` | POST | Clear all logs for a server |
| `/api/servers/{name}/logs/rotate` | POST | Rotate current log (start new file) |
| `/api/logs/clear` | POST | Clear logs for all servers |

### Analytics

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/servers/{name}/analytics` | GET | Get boot analytics for a server |
| `/api/analytics` | GET | Get analytics for all servers |

### Utilities

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/version` | GET | Get server version |
| `/api/lookup/mac/{mac}` | GET | Lookup server by MAC address |

## Web Interface

Access the web UI at `http://console.g11.lo/`

### Features

- **Live Tab**: Real-time terminal with xterm.js, supports selection and copy
- **Logs Tab**: Browse historical logs with vertical scrubber for navigation
- **Analytics Tab**: Boot timing, OS detection, network interface events
- **Server Tabs**: Quick switching between servers with status indicators

### Keyboard Shortcuts

- `Ctrl+C` / `Cmd+C`: Copy selected text from terminal
- Click and drag: Select text in terminal

## go-sol Library

The native SOL implementation is in the separate `go-sol` package:

```go
import "github.com/gwest/go-sol"

// Create session
session := sol.New(sol.Config{
    Host:     "192.168.11.10",
    Port:     623,
    Username: "ADMIN",    // Example only - change to your credentials
    Password: "ADMIN",    // Example only - change to your credentials
    Timeout:  30 * time.Second,
})

// Connect
err := session.Connect(ctx)

// Read console output
for data := range session.Read() {
    fmt.Print(string(data))
}
```

### Key Features

- IPMI v2.0 RMCP+ authentication (RAKP)
- HMAC-SHA1 integrity and authentication
- Queue-based buffering (10,000 packets) for bursty boot output
- Automatic ACK handling
- Proper session teardown

## Deployment

### MikroTik RouterOS

The console server runs as a container on MikroTik RouterOS:

```bash
# Import container image
/container/add file=console-server.tar interface=veth5 \
    root-dir=raid1/images/console.g11.lo \
    mounts=console.g11.lo.0,console.g11.lo.1 \
    logging=yes start-on-boot=yes

# Create volume mounts
/container/mounts/add name=console.g11.lo.0 \
    src=/raid1/volumes/console.g11.lo.0 dst=/var/lib/data
/container/mounts/add name=console.g11.lo.1 \
    src=/raid1/volumes/console.g11.lo.1 dst=/root

# Start container
/container/start [find name=console.g11.lo]
```

### Docker/Podman

```bash
podman run -d --name console-server \
    -p 80:80 \
    -v /data:/var/lib/data \
    console-server:latest
```

## Troubleshooting

### No Console Output

1. Check BMC connectivity: `ping 192.168.11.10`
2. Verify IPMI credentials in config
3. Check SOL is enabled on BMC: `ipmitool -I lanplus -H 192.168.11.10 -U <user> -P <pass> sol info`
4. View container logs for connection errors

### Connection Timeouts

- Ensure UDP port 623 is accessible from container to BMC network
- Check for firewall rules blocking IPMI traffic
- Verify BMC SOL configuration (baud rate, privilege level)

### High Memory Usage

The go-sol library buffers up to 10,000 packets per server during bursty output (boot sequences). This is by design to prevent data loss. Memory usage will stabilize after boot completes.

## Dependencies

### Go Modules

- `github.com/gorilla/mux` - HTTP router
- `github.com/sirupsen/logrus` - Structured logging
- `gopkg.in/yaml.v3` - YAML configuration
- `github.com/gwest/go-sol` - Native IPMI SOL library (local)

### Frontend

- Bootstrap 5.3 - UI framework
- xterm.js 5.3 - Terminal emulator
- htmx - Dynamic HTML updates

## Version History

- **1.2.0**: Native Go SOL implementation, scratch container, queue-based buffering
- **1.1.0**: Analytics engine, boot detection, htmx-based UI
- **1.0.0**: Initial release with ipmitool backend
