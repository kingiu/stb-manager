# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GK6323V100C set-top-box remote management + OTA (Over-The-Air) update system. Two Go services communicate via MQTT (Eclipse Mosquitto broker): a server-side manager with REST API + web UI, and a device-side agent running on Android-based STB hardware.

## Architecture

```
┌──────────────────────────────┐          ┌──────────────────────────────┐
│  STB-Manager (server/)       │          │  GK-Agent (agent/)           │
│  - REST API on :8080         │◄─MQTT──►│  - MQTT client on device     │
│  - Web UI (index.html)       │  broker  │  - Registration, heartbeat,  │
│  - OTA file serving (/ota/)  │          │    command response, OTA     │
│  - Device state tracking     │          │                              │
└──────────────────────────────┘          └──────────────────────────────┘
```

### MQTT Topics

| Topic | Direction | Purpose |
|-------|-----------|---------|
| `devices/{serial}/register` | device→server | Device online registration |
| `devices/{serial}/heartbeat` | device→server | Heartbeat (60s interval) |
| `devices/{serial}/command` | server→device | Command dispatch |
| `devices/{serial}/ota/check` | device→server | OTA version check |
| `devices/{serial}/ota/result` | device→server | OTA result report |
| `devices/{serial}/status` | device→server | Online/offline status |

## Common Development Commands

### Build

```bash
# Server binary
cd server && go build -o stb-manager .

# Agent binary (cross-compile for ARM device)
cd agent && GOOS=linux GOARCH=arm GOARM=7 go build -o gk-agent-arm .
```

### Run (locally without Docker)

```bash
# Server (requires mosquitto running on localhost:1883)
cd server && go run .
# Environment: MQTT_BROKER, MQTT_USER, MQTT_PASS, HTTP_PORT, OTA_DIR

# Agent
cd agent && go run .
# Environment: MQTT_BROKER, MQTT_USER, MQTT_PASS, DEVICE_SERIAL, OTA_SERVER
```

### Docker Compose

```bash
# Full stack (mosquitto + stb-manager)
docker-compose up -d

# STB-manager only (mosquitto assumed pre-existing)
docker-compose -f docker-compose-stb.yml up -d

# Build Docker image for stb-manager
docker build -f server/Dockerfile -t stb-manager server/
```

### Setup mosquitto credentials

```bash
docker exec mosquitto mosquitto_passwd -b /mosquitto/config/passwd admin admin@manager
docker restart mosquitto
```

### Deploy agent to ROM

```bash
./scripts/deploy.sh
# Copies agent/gk-agent-arm → system/system/system/bin/gk-agent
# Creates init service definition at system/system/etc/init/gk-agent.rc
```

## Key Files

- `server/main.go` — Server: REST API routes, MQTT subscription, device state, OTA push, web UI template
- `agent/main.go` — Agent: MQTT pub/sub, command handler, OTA download+flash, heartbeat
- `server/index.html` — Web UI (embedded via `//go:embed`)
- `server/mosquitto.conf` / `server/acl.conf` — Mosquitto broker config and ACL rules
- `docker-compose.yml` — Full stack (mosquitto + stb-manager)
- `docker-compose-stb.yml` — STB-manager only (no mosquitto service)

## API Endpoints

- `GET /` — Web UI dashboard
- `GET /api/devices` — Device list with online status
- `POST /api/device/{serial}/command` — Dispatch MQTT command
- `POST /api/device/{serial}/reboot` — Remote reboot
- `POST /api/device/{serial}/restart` — Restart agent process
- `POST /api/device/{serial}/shell` — Execute shell command
- `POST /api/device/{serial}/log` — Get kernel log (dmesg)
- `GET /api/ota/list` — List available OTA ROM files
- `POST /api/ota/push` — Trigger OTA update (all or specific device). Payload: `{"serial":"...","firmware":"v1.2.zip","sha256":"..."}`
- `GET /api/ota/tasks` — List all OTA tasks
- `DELETE /api/ota/tasks` — Cancel current OTA task
- `GET /api/ota/task/{id}` — Get task progress/status
- `POST /api/ota/upload` — Upload new OTA ROM file
- `GET /ota/{filename}` — Serve OTA ROM download

## OTA Upgrade Features

### Agent-side (device)
- **Specified firmware**: `ota_push` payload supports `{"firmware":"v1.2.zip","sha256":"abc..."}`
- **SHA256 verification**: Computes hash of downloaded ROM and compares against expected value
- **Progress reporting**: Periodic `ota_result` messages with `status: "progress"`, including `%` complete, bytes downloaded
- **State persistence**: `OTAState` saved to `/data/gk-agent-ota.state` — survives reboots
- **Auto-recovery on reconnect**: When MQTT reconnects after an interrupted OTA, agent reports its state to server
- **Recovery trigger**: Copies ROM to `/cache/ota-update.zip`, sets `persist.sys.ota.trigger` and `sys.recovery.update` props
- **Auto-rollback**: On `error` phase, a new `ota_push` auto-triggers rollback to previous slot / clean cache

### Server-side
- **OTA task tracking**: Each push creates an `OTATask` with ID, status lifecycle (`pending → pushed → in_progress → done/error`), device list
- **Firmware validation**: Rejects push if specified ROM file doesn't exist
- **SHA256 computation**: `GET /api/ota/list` computes and returns SHA256 for each ROM file (under 2GB)
- **Task cancellation**: `DELETE /api/ota/tasks` cancels current in-progress task
- **Progress aggregation**: OTA result messages from devices update the current task's `Progress` field

## Notes

- Both server and agent use `github.com/eclipse/paho.mqtt.golang` for MQTT
- Default Go version: 1.24.4 (per go.mod)
- Server uses `//go:embed index.html` for the embedded web UI
- OTA flow: agent downloads `/ota/latest.zip` from server, copies to `/cache/`, triggers recovery reboot
- Pre-compiled agent binaries: `agent/gk-agent-arm` (ARM), `agent/gk-agent` (x86/localhost)
- Pre-compiled server binary: `server/stb-manager` (built for target platform)
- No unit tests in the repository yet
