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
