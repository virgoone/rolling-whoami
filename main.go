package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed VERSION
var version string

var revision = "unknown"

type app struct {
	instance     string
	hostname     string
	started      time.Time
	startupDelay time.Duration
	draining     atomic.Bool
	requests     atomic.Uint64
	waitSlots    chan struct{}
}

func newApp(delay time.Duration) *app {
	hostname, _ := os.Hostname()
	return &app{instance: rand.Text(), hostname: hostname, started: time.Now(), startupDelay: delay, waitSlots: make(chan struct{}, 32)}
}

func (a *app) ready() bool {
	return !a.draining.Load() && time.Since(a.started) >= a.startupDelay
}

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !a.ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	})
	mux.HandleFunc("GET /{$}", a.whoami)
	mux.HandleFunc("GET /wait", a.wait)
	mux.HandleFunc("GET /preview", previewPage)
	return mux
}

func (a *app) whoami(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-App-Version", strings.TrimSpace(version))
	w.Header().Set("X-Instance-ID", a.instance)
	// Never echo environment variables, cookies or authorization headers.
	_ = json.NewEncoder(w).Encode(struct {
		Version  string    `json:"version"`
		Revision string    `json:"revision"`
		Instance string    `json:"instance_id"`
		Hostname string    `json:"hostname"`
		Started  time.Time `json:"started_at"`
		Uptime   float64   `json:"uptime_seconds"`
		Request  uint64    `json:"request_number"`
		Method   string    `json:"method"`
		Path     string    `json:"path"`
	}{strings.TrimSpace(version), revision, a.instance, a.hostname, a.started.UTC(), time.Since(a.started).Seconds(), a.requests.Add(1), r.Method, r.URL.Path})
}

func (a *app) wait(w http.ResponseWriter, r *http.Request) {
	d := 5 * time.Second
	if raw := r.URL.Query().Get("duration"); raw != "" {
		var err error
		d, err = time.ParseDuration(raw)
		if err != nil || d < 0 || d > 20*time.Second {
			http.Error(w, "duration must be between 0s and 20s", http.StatusBadRequest)
			return
		}
	}
	select {
	case a.waitSlots <- struct{}{}:
		defer func() { <-a.waitSlots }()
	default:
		http.Error(w, "too many waiting requests", http.StatusTooManyRequests)
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		return
	case <-timer.C:
		a.whoami(w, r)
	}
}

func run() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid PORT")
	}
	delay := time.Duration(0)
	if raw := os.Getenv("STARTUP_DELAY"); raw != "" {
		delay, err = time.ParseDuration(raw)
		if err != nil || delay < 0 || delay > time.Minute {
			return fmt.Errorf("STARTUP_DELAY must be between 0s and 1m")
		}
	}
	a := newApp(delay)
	srv := &http.Server{Addr: ":" + port, Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	log.Printf("started version=%s instance=%s port=%s", strings.TrimSpace(version), a.instance, port)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	a.draining.Store(true)
	log.Printf("draining instance=%s", a.instance)
	shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
		return err
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Printf("stopped instance=%s", a.instance)
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// previewPage is a static visual fixture for Berth's HTML preview toolbar.
func previewPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Rolling · Blue preview</title><style>
*{box-sizing:border-box}body{margin:0;background:#f4f6fa;color:#14213d;font:16px system-ui,-apple-system,sans-serif}main{max-width:960px;margin:10vh auto;padding:32px}header{display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #dbe2ed;padding-bottom:24px}.mark{font-weight:800;letter-spacing:3px}.badge{color:#2456d7;background:#e5edff;padding:8px 14px;border-radius:24px;font-size:13px}h1{font-size:clamp(40px,7vw,72px);letter-spacing:-3px;line-height:1.05;margin:64px 0 24px}h1 span{color:#2456d7}p{color:#617089;line-height:1.8;max-width:640px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:16px;margin:40px 0}.item{background:white;border:1px solid #dbe2ed;border-radius:16px;padding:24px}.item small{display:block;color:#617089;margin-bottom:12px}.item strong{font-size:21px}a,button{display:inline-block;background:#2456d7;color:white;border:0;border-radius:10px;padding:13px 18px;text-decoration:none;font:inherit;cursor:pointer}pre{background:#14213d;color:#e5edff;padding:24px;border-radius:16px;overflow:auto;font-size:13px;line-height:1.7}footer{color:#617089;margin-top:24px;font-size:13px}</style></head><body><main><header><div class="mark">ROLLING / WHOAMI</div><div class="badge">Isolated preview · BLUE</div></header><h1>一个分支。<br><span>一个独立预览。</span></h1><p>这是 Blue 测试分支。页面与版本随分支部署，生产环境继续使用 main。底部的 Berth 工具栏由平台注入，可用来查看当前预览信息。</p><div class="grid"><div class="item"><small>测试版本</small><strong>preview-blue</strong></div><div class="item"><small>部署方式</small><strong>Rolling · 2 replicas</strong></div><div class="item"><small>健康检查</small><strong>/readyz</strong></div></div><button id="refresh">刷新实例信息</button> <a href="/">查看原始 JSON</a><pre id="identity">正在读取当前实例…</pre><footer>每次刷新可能命中不同副本；版本应始终属于当前预览分支。</footer></main><script>async function refresh(){try{const r=await fetch('/',{cache:'no-store'});if(!r.ok)throw new Error('HTTP '+r.status);document.getElementById('identity').textContent=JSON.stringify(await r.json(),null,2)}catch(e){document.getElementById('identity').textContent=e.message}}document.getElementById('refresh').addEventListener('click',refresh);refresh();</script></body></html>`)
}
