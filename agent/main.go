package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Config loaded from /etc/gk-agent.conf
type Config struct {
	MQTTBroker string
	MQTTUser   string
	MQTTPass   string
	Serial     string
	Heartbeat  int    // seconds
	OTAServer  string
	BootSlot   string // "a" or "b" for A/B partition
}

type DeviceInfo struct {
	Serial    string `json:"serial"`
	HWModel   string `json:"hw_model"`
	BuildID   string `json:"build_id"`
	Firmware  string `json:"firmware_version"`
	Platform  string `json:"platform"`
	Timestamp int64  `json:"ts"`
}

type Command struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Response struct {
	CommandID string      `json:"command_id"`
	Type      string      `json:"type"`
	Status    string      `json:"status"`
	Message   string      `json:"message,omitempty"`
	Payload   interface{} `json:"payload,omitempty"`
}

var config Config

func loadConfig() {
	config.MQTTBroker = getEnv("MQTT_BROKER", "tcp://up.aisxuexi.com:1883")
	config.MQTTUser = getEnv("MQTT_USER", "admin")
	config.MQTTPass = getEnv("MQTT_PASS", "admin@manager")
	config.Heartbeat = 60

	// Try to read serial from device property
	serial := getProp("ro.serialno")
	if serial == "" {
		serial = getEnv("DEVICE_SERIAL", "GK6323V100C-UNKNOWN")
	}
	config.Serial = serial

	// OTA server URL (download ROMs)
	config.OTAServer = getEnv("OTA_SERVER", "http://up.aisxuexi.com:8080")

	// A/B boot slot
	config.BootSlot = getEnv("BOOT_SLOT", "a")
	if config.BootSlot == "" {
		config.BootSlot = "a"
	}
}

func getProp(name string) string {
	// Android property getter
	cmd := exec.Command("getprop", name)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func runShell(cmd string) string {
	c := exec.Command("/system/bin/sh", "-c", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func getDeviceInfo() DeviceInfo {
	return DeviceInfo{
		Serial:    config.Serial,
		HWModel:   getProp("ro.product.model"),
		BuildID:   getProp("ro.build.display.id"),
		Firmware:  getProp("ro.build.version.release"),
		Platform:  "GK6323V100C",
		Timestamp: time.Now().Unix(),
	}
}

func main() {
	loadConfig()
	log.Printf("=== GK Agent Starting ===")
	log.Printf("Serial: %s", config.Serial)
	log.Printf("MQTT: %s", config.MQTTBroker)
	log.Printf("OTA: %s", config.OTAServer)

	// ---- MQTT Connection ----
	opts := mqtt.NewClientOptions()
	opts.AddBroker(config.MQTTBroker)
	opts.SetClientID("stb-" + config.Serial)
	opts.SetUsername(config.MQTTUser)
	opts.SetPassword(config.MQTTPass)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(30 * time.Second)
	opts.SetCleanSession(false)
	opts.SetWill("devices/"+config.Serial+"/status", "offline", 1, false)

	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("MQTT lost: %v, reconnecting...", err)
	})
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Println("MQTT connected")
		// Restore OTA state from prior run
		loadOTAState()
		// Publish registration immediately
		info := getDeviceInfo()
		data, _ := json.Marshal(info)
		c.Publish("devices/"+config.Serial+"/register", 1, false, data)
		// If there was a prior OTA in progress, report status
		if otaState.Phase != "idle" {
			log.Printf("Reporting prior OTA state on reconnect: phase=%s", otaState.Phase)
			go func() {
				time.Sleep(1 * time.Second)
				reportResult(c, "reconnect", otaState.Phase, "recovered state after reconnect",
					map[string]string{
						"firmware":    otaState.Firmware,
						"downloaded":  fmt.Sprintf("%d", otaState.Downloaded),
						"total":       fmt.Sprintf("%d", otaState.Total),
						"error":       otaState.LastError,
						"currentBuild": otaState.CurrentBuild,
						"targetBuild":  otaState.TargetBuild,
					})
			}()
		}
		// Start heartbeat
		go heartbeatLoop(c)
	})

	client := mqtt.NewClient(opts)
	if tok := client.Connect(); tok.WaitTimeout(10*time.Second) && tok.Error() != nil {
		log.Fatalf("MQTT connect failed: %v", tok.Error())
	}

	// Subscribe to commands and OTA
	client.Subscribe("devices/"+config.Serial+"/command", 1, onCommand)

	log.Println("Agent ready. Listening for commands...")

	// Keep running
	select {}
}

func heartbeatLoop(client mqtt.Client) {
	ticker := time.NewTicker(time.Duration(config.Heartbeat) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		client.Publish("devices/"+config.Serial+"/heartbeat", 1, false, []byte(`{"ts":`+fmt.Sprintf("%d", time.Now().Unix())+`}`))
	}
}

func onCommand(client mqtt.Client, msg mqtt.Message) {
	var cmd Command
	if err := json.Unmarshal(msg.Payload(), &cmd); err != nil {
		log.Printf("Bad command JSON: %v", err)
		return
	}
	log.Printf("Command received: type=%s id=%s", cmd.Type, cmd.ID)

	var result string
	var payload interface{}

	switch cmd.Type {
	case "ota_push":
		result, payload = handleOTA(client, cmd)
	case "ota_check":
		info := getDeviceInfo()
		resp := Response{
			CommandID: cmd.ID,
			Type:      "ota_check",
			Status:    "ok",
			Payload:   map[string]string{"current": info.BuildID, "update": "no_update"},
		}
		sendResponse(client, resp)
		result = "ok"
	case "reboot":
		go func() {
			time.Sleep(2 * time.Second)
			runShell("reboot")
		}()
		result = "rebooting..."
	case "restart":
		result = "restarting agent..."
		// Restart itself - just exit, init will restart us
		go func() {
			time.Sleep(1 * time.Second)
			os.Exit(0)
		}()
	case "get_log":
		// Read last 200 lines of kernel log
		out := runShell("dmesg | tail -200")
		payload = out
		result = "ok"
	case "shell":
		// Execute shell command (careful!)
		if cmd.Payload != nil {
			var req struct {
				Cmd string `json:"cmd"`
			}
			json.Unmarshal(cmd.Payload, &req)
			out := runShell(req.Cmd)
			payload = out
		}
		result = "ok"
	default:
		result = fmt.Sprintf("unknown command type: %s", cmd.Type)
	}

	resp := Response{
		CommandID: cmd.ID,
		Type:      cmd.Type,
		Status:    result,
		Message:   fmt.Sprintf("%v", payload),
	}
	sendResponse(client, resp)
}

// ============ OTA Update State ============

// OTA update state persisted across reboots
type OTAState struct {
	Firmware   string `json:"firmware"`
	ExpectedSha256 string `json:"expected_sha256"`
	Phase      string `json:"phase"` // "idle" | "downloading" | "verifying" | "flashing" | "rebooting" | "rollback" | "done" | "error"
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	LastError  string `json:"last_error,omitempty"`
	CurrentBuild   string `json:"current_build"`
	TargetBuild    string `json:"target_build"`
}

var (
	otaStatePath = "/data/gk-agent-ota.state"
	otaState     = OTAState{Phase: "idle"}
)

// saveOTAState persists OTA state to disk so it survives reboots
func saveOTAState() {
	f, err := os.OpenFile(otaStatePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("saveOTAState: failed to write state: %v", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.Encode(otaState)
	log.Printf("OTA state saved: phase=%s firmware=%s", otaState.Phase, otaState.Firmware)
}

// loadOTAState restores OTA state from disk
func loadOTAState() {
	f, err := os.Open(otaStatePath)
	if err != nil {
		log.Printf("No OTA state file, starting clean")
		return
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&otaState); err != nil {
		log.Printf("Corrupt OTA state, starting clean: %v", err)
		otaState = OTAState{Phase: "idle"}
		return
	}
	log.Printf("OTA state restored: phase=%s firmware=%s", otaState.Phase, otaState.Firmware)
}

func handleOTA(client mqtt.Client, cmd Command) (string, interface{}) {
	loadOTAState()

	// Parse the command payload for firmware name and optional SHA256
	var req struct {
		Firmware    string `json:"firmware"`
		Sha256      string `json:"sha256"`
		ForceReboot bool   `json:"reboot"`
	}
	if cmd.Payload != nil {
		json.Unmarshal(cmd.Payload, &req)
	}
	if req.Firmware == "" {
		req.Firmware = "latest.zip"
	}
	forceReboot := req.ForceReboot

	// If there is a prior failed update, try to rollback
	if otaState.Phase == "error" && otaState.Phase != "idle" {
		log.Printf("Prior OTA failed, attempting rollback from %s", otaState.CurrentBuild)
		go func() {
			rollbackResult := doRollback()
			resp := Response{
				CommandID: cmd.ID,
				Type:      "ota_rollback",
				Status:    rollbackResult,
			}
			sendResponse(client, resp)
		}()
		return "rolling_back", map[string]string{"from": otaState.CurrentBuild}
	}

	// If already updating, reject
	if otaState.Phase != "idle" {
		return "already_updating", map[string]string{"phase": otaState.Phase}
	}

	romURL := config.OTAServer + "/ota/" + req.Firmware
	romPath := "/tmp/ota-update.zip"
	cachePath := "/cache/ota-update.zip"

	// Phase: downloading
	otaState = OTAState{
		Firmware:     req.Firmware,
		ExpectedSha256: req.Sha256,
		Phase:        "downloading",
		CurrentBuild: otaState.CurrentBuild,
	}
	saveOTAState()

	// Run download+verify+flash in background
	go func() {
		// Step 1: Download
		log.Printf("[%s] Starting OTA download from %s", otaState.Firmware, romURL)
		respHTTP, err := HTTPGet(romURL)
		if err != nil {
			log.Printf("OTA download failed: %v", err)
			otaState.Phase = "error"
			otaState.LastError = "download failed: " + err.Error()
			saveOTAState()
			reportResult(client, cmd.ID, "error", "download failed", map[string]string{"error": otaState.LastError})
			return
		}
		total := int64(0)
		if respHTTP.ContentLength > 0 {
			total = respHTTP.ContentLength
		}
		otaState.Total = total
		otaState.Downloaded = 0
		saveOTAState()

		f, err := os.Create(romPath)
		if err != nil {
			log.Printf("Cannot create ROM file: %v", err)
			respHTTP.Body.Close()
			otaState.Phase = "error"
			otaState.LastError = "create file: " + err.Error()
			saveOTAState()
			reportResult(client, cmd.ID, "error", "create file", map[string]string{"error": otaState.LastError})
			return
		}

		// Stream with progress reporting every 5 seconds
		type downloadProgress struct {
			downloaded int64
			done       bool
			err        error
			file       *os.File
			respBody   io.ReadCloser
		}
		ch := make(chan downloadProgress, 1)

		go func() {
			var dl int64
			lastReport := time.Now()
			buf := make([]byte, 32*1024)
			for {
				n, readErr := respHTTP.Body.Read(buf)
				if n > 0 {
					if _, writeErr := f.Write(buf[:n]); writeErr != nil {
						ch <- downloadProgress{err: writeErr, file: f, respBody: respHTTP.Body}
						return
					}
					dl += int64(n)
					otaState.Downloaded = dl
					saveOTAState()
				}
				if time.Since(lastReport) >= 5*time.Second || readErr != nil {
					lastReport = time.Now()
					go func() {
						otaState.Total = total
						if total == 0 {
							otaState.Total = dl
						}
						otaState.Downloaded = dl
						pct := float64(dl) / float64(otaState.Total) * 100
						reportResult(client, cmd.ID, "progress", "downloading",
							map[string]interface{}{"percent": pct, "downloaded": dl, "total": otaState.Total})
					}()
				}
				if readErr != nil {
					f.Close()
					respHTTP.Body.Close()
					ch <- downloadProgress{downloaded: dl, done: true, file: f, respBody: nil}
					return
				}
			}
		}()

		dp := <-ch
		respHTTP.Body.Close()
		f.Close()
		otaState.Downloaded = dp.downloaded
		otaState.Total = dp.downloaded

		if dp.err != nil {
			log.Printf("Download stream error: %v", dp.err)
			otaState.Phase = "error"
			otaState.LastError = "stream error: " + dp.err.Error()
			os.Remove(romPath)
			saveOTAState()
			reportResult(client, cmd.ID, "error", "download failed", map[string]string{"error": otaState.LastError})
			return
		}
		log.Printf("ROM downloaded: %d bytes", dp.downloaded)

		// Phase: verifying
		otaState.Phase = "verifying"
		saveOTAState()
		reportResult(client, cmd.ID, "progress", "verifying", nil)

		// Verify SHA256 if provided
		if req.Sha256 != "" {
			h, err := sha256File(romPath)
			if err != nil {
				log.Printf("SHA256 compute error: %v", err)
				otaState.Phase = "error"
				otaState.LastError = "hash: " + err.Error()
				os.Remove(romPath)
				saveOTAState()
				reportResult(client, cmd.ID, "error", "verify failed", map[string]string{"error": otaState.LastError})
				return
			}
			if strings.ToLower(h) != strings.ToLower(req.Sha256) {
				log.Printf("SHA256 mismatch: got %s, expected %s", h, req.Sha256)
				otaState.Phase = "error"
				otaState.LastError = "hash mismatch"
				os.Remove(romPath)
				saveOTAState()
				reportResult(client, cmd.ID, "error", "hash mismatch", nil)
				return
			}
			log.Printf("SHA256 verified OK")
		} else {
			log.Printf("No SHA256 provided, skipping verification")
		}

		// Phase: flashing
		otaState.Phase = "flashing"
		otaState.TargetBuild = getProp("ro.build.display.id")
		saveOTAState()
		reportResult(client, cmd.ID, "progress", "flashing", nil)

		// Step 2: Flash — copy to cache and trigger recovery mode
		result := runShell(fmt.Sprintf("cp %s %s && sync", romPath, cachePath))
		if !strings.Contains(result, "ok") {
			log.Printf("Copy to cache failed: %s", result)
			otaState.Phase = "error"
			otaState.LastError = "flash copy failed"
			saveOTAState()
			reportResult(client, cmd.ID, "error", "flash failed", map[string]string{"detail": result})
			return
		}

		// Trigger recovery by setting the property that recovery daemon watches
		runShell("setprop persist.sys.ota.trigger 1")
		runShell("setprop sys.recovery.update /cache/ota-update.zip")

		// Phase: rebooting
		otaState.Phase = "rebooting"
		otaState.TargetBuild = getProp("ro.build.display.id")
		saveOTAState()
		reportResult(client, cmd.ID, "progress", "rebooting",
			map[string]string{"from": otaState.CurrentBuild, "to": req.Firmware})

		if forceReboot {
			time.Sleep(2 * time.Second)
			runShell("reboot")
		}

		otaState.Phase = "done"
		otaState.LastError = ""
		saveOTAState()
		reportResult(client, cmd.ID, "ok", "OTA package applied, waiting for reboot",
			map[string]string{"firmware": req.Firmware})
	}()

	return "started", map[string]string{"firmware": req.Firmware, "status": "downloading"}
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// doRollback attempts to restore the previous ROM state
func doRollback() string {
	log.Println("=== Rollback: restoring previous firmware ===")
	// Try to boot from backup partition (A/B partition scheme)
	if _, err := os.Stat("/dev/block/bootdevice/by-name/recovery_b"); err == nil {
		runShell("setprop ro.boot.slot _b")
		runShell("reboot")
		return "rollback_to_backup"
	}
	// Fallback: remove OTA package and let recovery re-rollback
	runShell("rm -f /cache/ota-update.zip")
	runShell("setprop persist.sys.ota.trigger 0")
	runShell("reboot")
	return "rollback_cleanup"
}

func reportResult(client mqtt.Client, commandID, status, message string, payload interface{}) {
	resp := Response{
		CommandID: commandID,
		Type:      "ota_result",
		Status:    status,
		Message:   message,
		Payload:   payload,
	}
	sendResponse(client, resp)
}

func sendResponse(client mqtt.Client, resp Response) {
	data, _ := json.Marshal(resp)
	topic := fmt.Sprintf("devices/%s/ota/result", config.Serial)
	client.Publish(topic, 1, false, data)
	log.Printf("Sent response: %s", string(data))
}

func HTTPGet(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 300 * time.Second}
	return client.Get(url)
}
