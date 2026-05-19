package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type serveConfig struct {
	Host          string  `yaml:"host"`
	Port          int     `yaml:"port"`
	NGPULayers    int     `yaml:"n_gpu_layers"`
	CtxSize       int     `yaml:"ctx_size"`
	Threads       int     `yaml:"threads"`
	Temp          float64 `yaml:"temp"`
	TopP          float64 `yaml:"top_p"`
	TopK          *int    `yaml:"top_k"`
	RepeatPenalty float64 `yaml:"repeat_penalty"`
	CacheTypeK    string  `yaml:"cache_type_k"`
	CacheTypeV    string  `yaml:"cache_type_v"`
	FlashAttn     bool    `yaml:"flash_attn"`
	Jinja         bool    `yaml:"jinja"`
	BatchSize     *int    `yaml:"batch_size"`
	UBatchSize    *int    `yaml:"ubatch_size"`
	ContBatching  bool    `yaml:"cont_batching"`
	Mmap          *bool   `yaml:"mmap"`
}

type modelConfig struct {
	Name     string      `yaml:"name"`
	Repo     string      `yaml:"repo"`
	Include  string      `yaml:"include"`
	LocalDir string      `yaml:"local_dir"`
	Default  bool        `yaml:"default"`
	Serve    serveConfig `yaml:"serve"`
}

type rootConfig struct {
	Models []modelConfig `yaml:"models"`
}

const sharedPort = 18888

var (
	currentModel  string
	serverProcess *exec.Cmd
	serverMu      sync.Mutex
	models        map[string]modelConfig
	defaultModel  string
)

func loadConfig() {
	scriptDir := filepath.Dir(os.Args[0])
	projectDir := filepath.Dir(scriptDir)

	baseFile := filepath.Join(projectDir, "../models.yaml")
	osFile := filepath.Join(projectDir, "config", fmt.Sprintf("%s.yaml", runtime.GOOS))

	models = make(map[string]modelConfig)

	data, err := os.ReadFile(baseFile)
	if err != nil {
		log.Fatalf("Cannot read %s: %v", baseFile, err)
	}

	var cfg rootConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Cannot parse %s: %v", baseFile, err)
	}

	for _, m := range cfg.Models {
		models[m.Name] = m
		if m.Default {
			defaultModel = m.Name
		}
	}

	if overlayData, err := os.ReadFile(osFile); err == nil {
		var overlay rootConfig
		if err := yaml.Unmarshal(overlayData, &overlay); err == nil {
			for _, om := range overlay.Models {
				if bm, ok := models[om.Name]; ok {
					bm.Serve = mergeServe(bm.Serve, om.Serve)
					models[om.Name] = bm
				}
			}
		}
	}
}

func mergeServe(base, overlay serveConfig) serveConfig {
	if overlay.Host != "" {
		base.Host = overlay.Host
	}
	if overlay.NGPULayers > 0 {
		base.NGPULayers = overlay.NGPULayers
	}
	if overlay.CtxSize > 0 {
		base.CtxSize = overlay.CtxSize
	}
	if overlay.Threads > 0 {
		base.Threads = overlay.Threads
	}
	if overlay.Temp > 0 {
		base.Temp = overlay.Temp
	}
	if overlay.TopP > 0 {
		base.TopP = overlay.TopP
	}
	if overlay.TopK != nil {
		base.TopK = overlay.TopK
	}
	if overlay.RepeatPenalty > 0 {
		base.RepeatPenalty = overlay.RepeatPenalty
	}
	if overlay.CacheTypeK != "" {
		base.CacheTypeK = overlay.CacheTypeK
	}
	if overlay.CacheTypeV != "" {
		base.CacheTypeV = overlay.CacheTypeV
	}
	if overlay.FlashAttn {
		base.FlashAttn = true
	}
	if overlay.Jinja {
		base.Jinja = true
	}
	if overlay.BatchSize != nil {
		base.BatchSize = overlay.BatchSize
	}
	if overlay.UBatchSize != nil {
		base.UBatchSize = overlay.UBatchSize
	}
	if overlay.ContBatching {
		base.ContBatching = true
	}
	if overlay.Mmap != nil {
		base.Mmap = overlay.Mmap
	}
	return base
}

func findModelFile(localDir string) (string, error) {
	dir := filepath.Clean(localDir)
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, dir[1:])
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.gguf"))
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("no .gguf file found in %s", dir)
	}
	return files[0], nil
}

func buildArgs(cfg modelConfig) []string {
	modelFile, err := findModelFile(cfg.LocalDir)
	if err != nil {
		log.Fatalf("Cannot find model file: %v", err)
	}

	s := cfg.Serve
	args := []string{
		"--model", modelFile,
		"--host", s.Host,
		"--port", fmt.Sprintf("%d", sharedPort),
		"-ngl", fmt.Sprintf("%d", s.NGPULayers),
		"--ctx-size", fmt.Sprintf("%d", s.CtxSize),
		"-t", fmt.Sprintf("%d", s.Threads),
		"--temp", fmt.Sprintf("%.2f", s.Temp),
		"--top-p", fmt.Sprintf("%.2f", s.TopP),
		"--repeat-penalty", fmt.Sprintf("%.2f", s.RepeatPenalty),
	}

	if s.TopK != nil {
		args = append(args, "--top-k", fmt.Sprintf("%d", *s.TopK))
	}
	if s.CacheTypeK != "" {
		args = append(args, "--cache-type-k", s.CacheTypeK)
	}
	if s.CacheTypeV != "" {
		args = append(args, "--cache-type-v", s.CacheTypeV)
	}
	if s.FlashAttn {
		args = append(args, "--flash-attn", "on")
	}
	if s.Jinja {
		args = append(args, "--jinja")
	}
	if s.BatchSize != nil {
		args = append(args, "--batch-size", fmt.Sprintf("%d", *s.BatchSize))
	}
	if s.UBatchSize != nil {
		args = append(args, "--ubatch-size", fmt.Sprintf("%d", *s.UBatchSize))
	}
	if s.ContBatching {
		args = append(args, "--cont-batching")
	}
	if s.Mmap != nil {
		if !*s.Mmap {
			args = append(args, "--no-mmap")
		} else {
			args = append(args, "--mmap")
		}
	}

	return args
}

func stopServer() {
	if serverProcess != nil && serverProcess.Process != nil {
		log.Printf("Stopping server for '%s'...", currentModel)
		serverProcess.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- serverProcess.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			serverProcess.Process.Kill()
			<-done
		}
		log.Println("Server stopped, VRAM freed.")
	}
	serverProcess = nil
	currentModel = ""
}

func waitReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("server did not become ready in time")
		default:
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", sharedPort))
			if err == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(1 * time.Second)
		}
	}
}

func startServer(modelName string) error {
	serverMu.Lock()
	defer serverMu.Unlock()

	if currentModel == modelName && serverProcess != nil && serverProcess.Process != nil && serverProcess.Process.Pid > 0 {
		return nil
	}

	stopServer()

	cfg, ok := models[modelName]
	if !ok {
		return fmt.Errorf("unknown model: %s", modelName)
	}

	args := buildArgs(cfg)
	log.Printf("Starting server for '%s'...", modelName)

	serverProcess = exec.Command("llama-server", args...)
	serverProcess.Stdout = nil
	serverProcess.Stderr = nil
	if err := serverProcess.Start(); err != nil {
		return fmt.Errorf("failed to start llama-server: %w", err)
	}

	currentModel = modelName

	if err := waitReady(); err != nil {
		stopServer()
		return err
	}

	log.Printf("Server for '%s' ready.", modelName)
	return nil
}

func resolveModel(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts {
		if part != "" && models[part] != (modelConfig{}) {
			return part
		}
	}
	return defaultModel
}

func stripPrefix(path, model string) string {
	idx := strings.Index(path, "/"+model+"/")
	if idx >= 0 {
		return path[idx+len(model)+2:]
	}
	idx = strings.Index(path, model+"/")
	if idx == 0 {
		return path[len(model)+1:]
	}
	return path
}

type statusResponse struct {
	CurrentModel  string `json:"current_model"`
	ServerRunning bool   `json:"server_running"`
}

type switchResponse struct {
	CurrentModel string `json:"current_model"`
	Message      string `json:"message"`
}

func handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/status" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statusResponse{
			CurrentModel:  currentModel,
			ServerRunning: serverProcess != nil,
		})
		return
	}

	if strings.HasPrefix(path, "/switch/") && r.Method == http.MethodPost {
		modelName := strings.TrimPrefix(path, "/switch/")
		if err := startServer(modelName); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(switchResponse{
			CurrentModel: currentModel,
			Message:      fmt.Sprintf("Switched to '%s'", modelName),
		})
		return
	}

	modelName := resolveModel(path)
	realPath := stripPrefix(path, modelName)

	if err := startServer(modelName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	upstream := fmt.Sprintf("http://127.0.0.1:%d/%s", sharedPort, realPath)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstream, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for k, vv := range r.Header {
		if k != "Host" {
			req.Header[k] = vv
		}
	}

	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: false,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		if strings.ToLower(k) != "transfer-encoding" {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
	}

	w.WriteHeader(resp.StatusCode)

	var copyBuf []byte
	bufSize := 32 * 1024
	if r.Method == http.MethodGet {
		bufSize = 64 * 1024
	}

	buf := make([]byte, bufSize)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			flusher, ok := w.(http.Flusher)
			if !ok {
				copy(copyBuf, buf[:n])
				_, _ = w.Write(buf[:n])
			} else {
				_, _ = w.Write(buf[:n])
				flusher.Flush()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Stream error: %v", err)
			break
		}
	}
}

func main() {
	loadConfig()

	log.SetFlags(log.Ltime)

	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		go func(s os.Signal) {
			<-make(chan os.Signal, 1)
			log.Println("\nShutting down orchestrator...")
			stopServer()
			os.Exit(0)
		}(sig)
	}

	// Actually set up signal handling properly
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("\nShutting down orchestrator...")
		stopServer()
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", 8082)
	log.Printf("Orchestrator listening on %s", addr)
	log.Printf("Available models: %v", func() []string {
		names := make([]string, 0, len(models))
		for n := range models {
			names = append(names, n)
		}
		return names
	}())

	if err := http.ListenAndServe(addr, http.HandlerFunc(handler)); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
