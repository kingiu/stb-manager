# GK6323V100C 自主远程管理 + OTA 方案

## 架构

```
┌──────────────────────────────────────────┐
│  容器 (你的服务器)                        │
│  ├─ mosquitto broker (1883)              │
│  ├─ stb-manager REST API (8080)          │
│  └─ OTA HTTP 文件服务 (8081)              │
└──────────────────────────────────────────┘
              ↑ MQTT + HTTP
┌──────────────────────────────────────────┐
│  机顶盒 GK6323V100C (ARM)                │
│  ├─ gk-agent (Go MQTT 客户端)            │
│  └─ 开机注册/心跳/命令响应/OTA            │
└──────────────────────────────────────────┘
```

## 项目结构

```
remote-manager/
├── docker-compose.yml      # 容器编排
├── server/
│   ├── main.go             # 管理后端 (REST API + MQTT)
│   ├── Dockerfile.mosquitto # mosquitto Dockerfile
│   ├── mosquitto.conf      # mosquitto 配置
│   └── acl.conf            # ACL 规则
├── agent/
│   ├── main.go             # 设备端 MQTT agent (Go)
│   ├── go.mod              # Go 依赖
│   └── gk-agent-arm        # 编译好的 ARM 二进制 (已部署)
└── scripts/
    ├── deploy.sh           # 一键部署脚本
    └── gk-agent-service.txt # init.rc 服务定义

system/                     # ROM 目录
└── system/system/bin/gk-agent  # 已部署到 ROM
```

## 快速启动

### 1. 准备服务器

```bash
cd remote-manager
docker-compose up -d

# 创建 mosquitto 密码
docker exec mosquitto mosquitto_passwd -b /mosquitto/config/passwd admin admin@manager
docker restart mosquitto

# 创建 OTA 目录
mkdir -p ota
```

### 2. 部署 OTA ROM 包

把你的 ROM 包放到 `ota/` 目录，命名为 `latest.zip`：

```bash
cp path/to/your/update.zip remote-manager/ota/latest.zip
```

### 3. 编译/部署 agent

```bash
# 如果需要重新编译:
cd remote-manager/agent
GOOS=linux GOARCH=arm GOARM=7 go build -o gk-agent-arm .

# 部署到 ROM:
cd ..
./scripts/deploy.sh
```

### 4. 启动管理后端

```bash
cd server
go build -o stb-manager .
./stb-manager

# 或直接用 docker-compose:
cd ..
docker-compose up -d stb-manager
```

## 使用管理 API

### 查看设备列表
```bash
curl http://localhost:8080/devices
```

### 下发 OTA 升级
```bash
curl -X POST http://localhost:8080/ota/push \
  -H "Content-Type: application/json" \
  -d '{"version":"1.0.2","reboot":true}'
```

### 指定设备升级
```bash
curl -X POST http://localhost:8080/ota/push \
  -H "Content-Type: application/json" \
  -d '{"serial":"GK6323V100C-UNKNOWN","version":"1.0.2","reboot":true}'
```

### 查看可用 ROM
```bash
curl http://localhost:8080/ota/list
```

### 重启设备
```bash
curl -X POST http://localhost:8080/devices/GK6323V100C-UNKNOWN/command \
  -H "Content-Type: application/json" \
  -d '{"type":"reboot"}'
```

### 执行 Shell 命令
```bash
curl -X POST http://localhost:8080/devices/GK6323V100C-UNKNOWN/command \
  -H "Content-Type: application/json" \
  -d '{"type":"shell","payload":{"cmd":"ls /system/app"}}'
```

## MQTT Topic 设计

| Topic | Direction | 用途 |
|-------|-----------|------|
| `devices/{serial}/register` | 设备→服务器 | 设备上线注册 |
| `devices/{serial}/heartbeat` | 设备→服务器 | 心跳 (60s) |
| `devices/{serial}/command` | 服务器→设备 | 下发命令 |
| `devices/{serial}/ota/check` | 设备→服务器 | OTA 版本查询 |
| `devices/{serial}/ota/result` | 设备→服务器 | OTA 结果上报 |
| `devices/{serial}/status` | 设备→服务器 | 在线状态 (离线时发布) |

## 支持的命令类型

| 命令 | payload | 说明 |
|------|---------|------|
| `reboot` | 无 | 重启设备 |
| `restart` | 无 | 重启 agent |
| `ota_push` | 无 | 开始 OTA 下载并刷入 |
| `ota_check` | 无 | 查询当前版本 |
| `get_log` | 无 | 获取内核日志 |
| `shell` | `{"cmd":"..."}` | 执行 shell 命令 |

## 安全建议

1. **修改 mosquitto 默认密码**: 在 `docker-compose.yml` 中设置 `MQTT_PASS` 环境变量
2. **使用 TLS**: mosquitto 支持 TLS，生产环境建议启用
3. **ACL 限制**: `acl.conf` 中已配置基本权限，生产环境需细化
4. **OTA 签名**: 生产 ROM 包应签名验证，防止被篡改

## 设备端环境

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `MQTT_BROKER` | `tcp://192.168.1.100:1883` | MQTT 服务器地址 |
| `MQTT_USER` | `admin` | MQTT 用户名 |
| `MQTT_PASS` | `admin@manager` | MQTT 密码 |
| `DEVICE_SERIAL` | `GK6323V100C-UNKNOWN` | 设备序列号 |
| `OTA_SERVER` | `http://192.168.1.100:8081` | OTA 文件服务器地址 |

## 已删除的组件

- `SHCMCC_MIGUOTTAD_PRO_BASE` — 咪咕 OTT 广告 APK

## 未修改的组件

- 讯飞 (SystemXiri) — 保留
- SW 平台遥测组件 — 保留
- 其他所有系统 APK — 保留
