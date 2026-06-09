package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// ============ Data Models ============

type Device struct {
	Serial      string    `json:"serial"`
	HWModel     string    `json:"hw_model"`
	BuildID     string    `json:"build_id"`
	FirmwareVer string    `json:"firmware_version"`
	Online      bool      `json:"online"`
	LastSeen    time.Time `json:"last_seen"`
}

type OTAFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Time string `json:"time"`
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

// ============ State ============

var (
	devices  = make(map[string]*Device)
	devMu    sync.RWMutex
	mqttCli  mqtt.Client
	otaDir   string
	otaBase  string
	// Templates
	tmpl *template.Template
)

// ============ HTML Templates ============

//go:embed index.html
var htmlPage []byte

func initTemplates() {
	// Register a function for date formatting
	funcMap := template.FuncMap{
		"since": func(t time.Time) string {
			d := time.Since(t)
			if d < time.Minute {
				return fmt.Sprintf("%d秒前", int(d.Seconds()))
			}
			if d < time.Hour {
				return fmt.Sprintf("%d分钟前", int(d.Minutes()))
			}
			if d < 24*time.Hour {
				return fmt.Sprintf("%d小时前", int(d.Hours()))
			}
			return t.Format("2006-01-02 15:04")
		},
		"humanSize": func(s int64) string {
			if s < 1024 {
				return fmt.Sprintf("%d B", s)
			}
			if s < 1024*1024 {
				return fmt.Sprintf("%.1f KB", float64(s)/1024)
			}
			return fmt.Sprintf("%.1f MB", float64(s)/(1024*1024))
		},
		"onlineBadge": func(b bool) string {
			if b {
				return `<span class="badge online">在线</span>`
			}
			return `<span class="badge offline">离线</span>`
		},
	}
	tmpl = template.Must(template.New("page").Funcs(funcMap).Parse(string(htmlPage)))
}

// ============ Handlers ============

func serveIndex(w http.ResponseWriter, r *http.Request) {
	_ = tmpl.Execute(w, nil)
}

func apiDevices(w http.ResponseWriter, r *http.Request) {
	devMu.RLock()
	deck := make([]Device, 0, len(devices))
	for _, d := range devices {
		deck = append(deck, *d)
	}
	devMu.RUnlock()
	sort.Slice(deck, func(i, j int) bool {
		return deck[i].LastSeen.After(deck[j].LastSeen)
	})
	jsonResp(w, 200, deck)
}

func apiDeviceCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	serial := strings.TrimPrefix(r.URL.Path, "/api/device/")
	if serial == "" {
		http.Error(w, "missing serial", 400)
		return
	}

	var cmd struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if cmd.Type == "" {
		http.Error(w, "missing type", 400)
		return
	}

	c := Command{ID: genID(), Type: cmd.Type}
	if cmd.Payload != nil {
		c.Payload = cmd.Payload
	}
	data, _ := json.Marshal(c)
	topic := fmt.Sprintf("devices/%s/command", serial)
	mqttCli.Publish(topic, 1, false, data)
	jsonResp(w, 200, map[string]string{"status": "queued", "id": c.ID})
}

func apiOtaList(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(otaDir)
	if err != nil {
		jsonResp(w, 200, []OTAFile{})
		return
	}
	files := make([]OTAFile, 0)
	for _, e := range entries {
		if !e.IsDir() {
			info, _ := e.Info()
			files = append(files, OTAFile{
				Name: e.Name(),
				Size: info.Size(),
				Time: info.ModTime().Format("2006-01-02 15:04"),
			})
		}
	}
	jsonResp(w, 200, files)
}

func apiOtaPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Serial   string `json:"serial"`
		Firmware string `json:"firmware"`
		Reboot   bool   `json:"reboot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", 400)
		return
	}

	cmd := Command{ID: genID(), Type: "ota_push"}
	if req.Firmware != "" {
		cmdData, _ := json.Marshal(map[string]string{"firmware": req.Firmware})
		cmd.Payload = cmdData
	} else {
		cmd.Payload = json.RawMessage(`{"firmware":"latest.zip"}`)
	}

	if req.Serial == "" {
		devMu.RLock()
		for s, d := range devices {
			if d.Online {
				mqttCli.Publish(fmt.Sprintf("devices/%s/command", s), 1, false, cmd.ID)
				_ = mqttCli.Publish(fmt.Sprintf("devices/%s/command", s), 1, false, []byte(`{"id":"`+genID()+`","type":"ota_push","payload":{"firmware":"`+req.Firmware+`"}}`))
			}
		}
		devMu.RUnlock()
	} else {
		data, _ := json.Marshal(cmd)
		mqttCli.Publish(fmt.Sprintf("devices/%s/command", req.Serial), 1, false, data)
	}
	jsonResp(w, 200, map[string]string{"status": "pushed"})
}

func apiRebootDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	serial := strings.TrimPrefix(r.URL.Path, "/api/device/")
	c := Command{ID: genID(), Type: "reboot"}
	data, _ := json.Marshal(c)
	mqttCli.Publish(fmt.Sprintf("devices/%s/command", serial), 1, false, data)
	jsonResp(w, 200, map[string]string{"status": "queued"})
}

func apiRestartAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	serial := strings.TrimPrefix(r.URL.Path, "/api/device/")
	c := Command{ID: genID(), Type: "restart"}
	data, _ := json.Marshal(c)
	mqttCli.Publish(fmt.Sprintf("devices/%s/command", serial), 1, false, data)
	jsonResp(w, 200, map[string]string{"status": "queued"})
}

func apiShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	serial := strings.TrimPrefix(r.URL.Path, "/api/device/")
	var req struct {
		Cmd string `json:"cmd"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	c := Command{ID: genID(), Type: "shell", Payload: json.RawMessage(`{"cmd":"` + strings.ReplaceAll(req.Cmd, `"`, `\"`) + `"}`)}
	data, _ := json.Marshal(c)
	mqttCli.Publish(fmt.Sprintf("devices/%s/command", serial), 1, false, data)
	jsonResp(w, 200, map[string]string{"status": "sent"})
}

func apiGetLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	serial := strings.TrimPrefix(r.URL.Path, "/api/device/")
	c := Command{ID: genID(), Type: "get_log"}
	data, _ := json.Marshal(c)
	mqttCli.Publish(fmt.Sprintf("devices/%s/command", serial), 1, false, data)
	jsonResp(w, 200, map[string]string{"status": "sent"})
}

// ============ MQTT Handler ============

func onMessage(client mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	parts := strings.SplitN(topic, "/", 4)
	if len(parts) < 4 {
		return
	}
	serial := parts[1]
	event := parts[3]

	devMu.Lock()
	d, ok := devices[serial]
	if !ok {
		d = &Device{Serial: serial, LastSeen: time.Now()}
		devices[serial] = d
	}
	devMu.Unlock()

	switch event {
	case "register":
		var reg struct {
			HWModel string `json:"hw_model"`
			BuildID string `json:"build_id"`
		}
		json.Unmarshal(msg.Payload(), &reg)
		devMu.Lock()
		d.HWModel = reg.HWModel
		d.BuildID = reg.BuildID
		d.FirmwareVer = d.BuildID
		d.Online = true
		d.LastSeen = time.Now()
		log.Printf("Registered: %s (%s) build=%s", serial, d.HWModel, d.BuildID)
		devMu.Unlock()

	case "heartbeat":
		devMu.Lock()
		d.Online = true
		d.LastSeen = time.Now()
		devMu.Unlock()

	case "ota/check":
		resp := Response{
			CommandID: genID(), Type: "ota_check", Status: "ok",
			Payload: map[string]string{"current": d.FirmwareVer, "update": "no_update"},
		}
		data, _ := json.Marshal(resp)
		client.Publish(fmt.Sprintf("devices/%s/ota/result", serial), 1, false, data)

	case "ota/result":
		var res Response
		json.Unmarshal(msg.Payload(), &res)
		log.Printf("OTA %s: %s %s", serial, res.Status, res.Message)
	}
}

// ============ OTA File Upload ============

func apiUploadOta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file: "+err.Error(), 400)
		return
	}
	defer file.Close()

	out, err := os.Create(otaDir + "/" + header.Filename)
	if err != nil {
		http.Error(w, "create failed: "+err.Error(), 500)
		return
	}
	defer out.Close()

	io.Copy(out, file)
	jsonResp(w, 200, map[string]string{"status": "uploaded", "name": header.Filename})
}

// ============ Main ============

func main() {
	mqttURL := getEnv("MQTT_BROKER", "tcp://localhost:1883")
	mqttUser := getEnv("MQTT_USER", "gk-admin")
	mqttPass := getEnv("MQTT_PASS", "gk-stb-2024")
	httpPort := getEnv("HTTP_PORT", "8080")
	otaPort := getEnv("OTA_PORT", "9080")
	otaDir = getEnv("OTA_DIR", "/opt/ota")

	os.MkdirAll(otaDir, 0755)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(mqttURL)
	opts.SetClientID("stb-manager")
	opts.SetUsername(mqttUser)
	opts.SetPassword(mqttPass)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("MQTT lost: %v", err)
	})
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Println("MQTT connected, subscribing...")
		topics := []string{
			"devices/+/register",
			"devices/+/heartbeat",
			"devices/+/command",
			"devices/+/ota/check",
			"devices/+/ota/result",
		}
		for _, t := range topics {
			c.Subscribe(t, 1, onMessage)
		}
	})

	mqttCli = mqtt.NewClient(opts)
	if tok := mqttCli.Connect(); tok.WaitTimeout(10*time.Second) && tok.Error() != nil {
		log.Fatalf("MQTT connect failed: %v", tok.Error())
	}

	initTemplates()

	mux := http.NewServeMux()

	// Web UI
	mux.HandleFunc("/", serveIndex)

	// REST API
	mux.HandleFunc("/api/devices", apiDevices)
	mux.HandleFunc("/api/device/", func(w http.ResponseWriter, r *http.Request) {
		_ = strings.TrimPrefix(r.URL.Path, "/api/device/")
		switch {
		case strings.HasSuffix(r.URL.Path, "/command"):
			apiDeviceCommand(w, r)
		case strings.HasSuffix(r.URL.Path, "/reboot"):
			apiRebootDevice(w, r)
		case strings.HasSuffix(r.URL.Path, "/restart"):
			apiRestartAgent(w, r)
		case strings.HasSuffix(r.URL.Path, "/shell"):
			apiShell(w, r)
		case strings.HasSuffix(r.URL.Path, "/log"):
			apiGetLog(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/ota/list", apiOtaList)
	mux.HandleFunc("/api/ota/push", apiOtaPush)
	mux.HandleFunc("/api/ota/upload", apiUploadOta)

	// OTA file serve
	otaHandler := http.FileServer(http.Dir(otaDir))

	go func() {
		log.Printf("OTA server: http://0.0.0.0:%s", otaPort)
		http.ListenAndServe(":"+otaPort, otaHandler)
	}()

	log.Printf("Web UI: http://0.0.0.0:%s", httpPort)
	log.Printf("MQTT: %s", mqttURL)
	log.Fatal(http.ListenAndServe(":"+httpPort, mux))
}

// ============ Helpers ============

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func jsonResp(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func genID() string {
	return fmt.Sprintf("cmd-%d", time.Now().UnixNano())
}
