package main

import (
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
	Heartbeat  int // seconds
	OTAServer  string
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
	config.MQTTBroker = getEnv("MQTT_BROKER", "tcp://192.168.1.100:1883")
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
	config.OTAServer = getEnv("OTA_SERVER", "http://192.168.1.100:8081")
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
		// Publish registration immediately
		info := getDeviceInfo()
		data, _ := json.Marshal(info)
		c.Publish("devices/"+config.Serial+"/register", 1, false, data)
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
		result, payload = handleOTA(client)
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

func handleOTA(client mqtt.Client) (string, interface{}) {
	log.Println("Starting OTA update...")

	// Step 1: Download ROM package
	romURL := config.OTAServer + "/latest.zip"
	log.Printf("Downloading ROM: %s", romURL)

	resp, err := httpGet(romURL)
	if err != nil {
		return "error", map[string]string{"error": err.Error()}
	}
	defer resp.Body.Close()

	romPath := "/tmp/ota-update.zip"
	f, err := os.Create(romPath)
	if err != nil {
		return "error", map[string]string{"error": err.Error()}
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(romPath)
		return "error", map[string]string{"error": err.Error()}
	}
	f.Close()

	log.Printf("ROM downloaded: %s", romPath)

	// Step 2: Flash via recovery
	// Option A: Use applypatch + package_extract_file pattern from original updater-script
	// Option B: Copy to cache and trigger recovery
	result := runShell(fmt.Sprintf("cp %s /cache/ota-update.zip && echo ok", romPath))

	log.Printf("Flash result: %s", result)

	resp2 := Response{
		CommandID: "ota",
		Type:      "ota_result",
		Status:    result,
		Message:   "OTA completed, rebooting in 5s",
	}
	sendResponse(client, resp2)

	go func() {
		time.Sleep(5 * time.Second)
		runShell("reboot")
	}()

	return "ok", result
}

func httpGet(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 300 * time.Second}
	return client.Get(url)
}

func sendResponse(client mqtt.Client, resp Response) {
	data, _ := json.Marshal(resp)
	topic := fmt.Sprintf("devices/%s/ota/result", config.Serial)
	client.Publish(topic, 1, false, data)
	log.Printf("Sent response: %s", string(data))
}
