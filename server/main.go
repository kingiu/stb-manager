package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"text/template"

	texttemplate "text/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
	BootSlot    string    `json:"boot_slot,omitempty"`
}

type OTAFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Time     string `json:"time"`
	SHA256    string `json:"sha256,omitempty"`
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

// OTATask tracks an in-flight OTA push
type OTATask struct {
	ID        string    `json:"id"`
	Firmware  string    `json:"firmware"`
	SHA256    string    `json:"sha256"`
	Status    string    `json:"status"` // "pending" | "pushed" | "in_progress" | "done" | "error"
	Devices   []string  `json:"devices"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Progress  string    `json:"progress"`
	LastError string    `json:"last_error,omitempty"`
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

	// OTA task tracking
	otaTasks   = make(map[string]*OTATask)
	otaTasksMu sync.Mutex
	currentTask *OTATask
)

// ============ HTML Templates ============

//go:embed index.html
var htmlPage []byte

// htmlPath is the fallback path for reading index.html at runtime
var htmlPath = "/app/index.html"

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
	// Try embedded HTML first, fall back to file if embed is empty
	htmlData := htmlPage
	if len(htmlData) == 0 {
		data, err := os.ReadFile(htmlPath)
		if err != nil {
			log.Fatalf("Failed to read index.html from %s: %v", htmlPath, err)
		}
		htmlData = data
	}
	// Use text/template to avoid html/template strict JS context validation
	// which fails on complex single-page app JavaScript
	tmpl = texttemplate.Must(texttemplate.New("page").Funcs(funcMap).Parse(string(htmlData)))
}

// ============ Handlers ============

func serveIndex(w http.ResponseWriter, r *http.Request) {
	log.Printf("serveIndex called, tmpl=%p", tmpl)
	if tmpl != nil {
		if err := tmpl.Execute(w, nil); err != nil {
			log.Printf("template execute error: %v", err)
			http.Error(w, err.Error(), 500)
		}
	} else {
		// Fallback: write raw embed
		if len(htmlPage) > 0 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(htmlPage)
		} else {
			w.WriteHeader(500)
			w.Write([]byte("template not initialized"))
		}
	}
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
			file := OTAFile{
				Name: e.Name(),
				Size: info.Size(),
				Time: info.ModTime().Format("2006-01-02 15:04"),
			}
			// Compute SHA256 for OTA files (only if small enough)
			if info.Size() < 2*1024*1024*1024 { // under 2GB
				file.SHA256 = computeSHA256(filepath.Join(otaDir, e.Name()))
			}
			files = append(files, file)
		}
	}
	jsonResp(w, 200, files)
}

func computeSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func apiOtaPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Serial   string `json:"serial"`
		Firmware string `json:"firmware"`
		SHA256   string `json:"sha256"`
		Reboot   bool   `json:"reboot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", 400)
		return
	}

	if req.Firmware == "" {
		req.Firmware = "latest.zip"
	}

	// Create OTA task
	task := OTATask{
		ID:        fmt.Sprintf("ota-%d", time.Now().UnixNano()),
		Firmware:  req.Firmware,
		SHA256:    req.SHA256,
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Validate firmware exists
	if _, err := os.Stat(filepath.Join(otaDir, req.Firmware)); os.IsNotExist(err) {
		jsonResp(w, 404, map[string]string{"error": "firmware not found: " + req.Firmware})
		return
	}

	if req.Serial == "" {
		// Push to all online devices
		devMu.RLock()
		for s, d := range devices {
			if d.Online {
				task.Devices = append(task.Devices, s)
			}
		}
		devMu.RUnlock()
	} else {
		task.Devices = append(task.Devices, req.Serial)
	}

	otaTasksMu.Lock()
	otaTasks[task.ID] = &task
	otaTasksMu.Unlock()

	// If there's an active task, mark as superseded
	otaTasksMu.Lock()
	if currentTask != nil && currentTask.Status != "done" {
		currentTask.Status = "superseded"
	}
	task.Status = "pushed"
	currentTask = &task
	otaTasksMu.Unlock()

	go func() {
		for _, serial := range task.Devices {
			if task.Status == "superseded" {
				break
			}
			cmd := Command{ID: genID(), Type: "ota_push",
				Payload: json.RawMessage(fmt.Sprintf(`{"firmware":"%s","sha256":"%s"}`, req.Firmware, req.SHA256))}
			data, _ := json.Marshal(cmd)
			topic := fmt.Sprintf("devices/%s/command", serial)
			mqttCli.Publish(topic, 1, false, data)
			log.Printf("OTA task %s: pushed to %s (firmware=%s)", task.ID, serial, req.Firmware)
		}
		otaTasksMu.Lock()
		task.Status = "in_progress"
		task.UpdatedAt = time.Now()
		otaTasksMu.Unlock()
	}()

	jsonResp(w, 200, map[string]string{"task_id": task.ID, "status": "pushed", "devices": fmt.Sprintf("%d", len(task.Devices))})
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

		// Update current task with progress from device
		otaTasksMu.Lock()
		if currentTask != nil && currentTask.Status == "in_progress" {
			currentTask.UpdatedAt = time.Now()
			currentTask.Progress = fmt.Sprintf("%s: %s (%s)", serial, res.Status, res.Message)
		}
		otaTasksMu.Unlock()
	}
}

// ============ OTA Task APIs ============

func apiOtaTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		otaTasksMu.Lock()
		tasks := make([]OTATask, 0, len(otaTasks))
		for _, t := range otaTasks {
			tasks = append(tasks, *t)
		}
		otaTasksMu.Unlock()
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		})
		jsonResp(w, 200, tasks)
	case http.MethodDelete:
		// Cancel current task
		otaTasksMu.Lock()
		if currentTask != nil && currentTask.Status == "in_progress" {
			currentTask.Status = "cancelled"
		}
		otaTasksMu.Unlock()
		jsonResp(w, 200, map[string]string{"status": "cancelled"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func apiOtaTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimPrefix(r.URL.Path, "/api/ota/task/")
	if taskID == "" {
		http.Error(w, "missing task id", 400)
		return
	}
	otaTasksMu.Lock()
	task, ok := otaTasks[taskID]
	otaTasksMu.Unlock()
	if !ok {
		http.Error(w, "task not found", 404)
		return
	}
	jsonResp(w, 200, task)
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

	out, err := os.Create(otaDir + "/" + filepath.Base(header.Filename))
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
	otaDir = getEnv("OTA_DIR", "/opt/ota")

	if err := os.MkdirAll(otaDir, 0755); err != nil {
		log.Fatalf("Failed to create OTA dir %s: %v", otaDir, err)
	}

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
	mux.HandleFunc("/api/ota/tasks", apiOtaTasks)
	mux.HandleFunc("/api/ota/task/", apiOtaTaskStatus)

	// OTA file serve — 通过 /ota/ 路径提供文件下载
	mux.Handle("/ota/", http.StripPrefix("/ota/", http.FileServer(http.Dir(otaDir))))

	log.Printf("Web UI: http://0.0.0.0:%s", httpPort)
	log.Printf("OTA files: http://0.0.0.0:%s/ota/", httpPort)
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
