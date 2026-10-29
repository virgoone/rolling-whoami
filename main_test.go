package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIdentityDoesNotEchoSecrets(t *testing.T) {
	a := newApp(0)
	r := httptest.NewRequest("GET", "/?token=secret-query", nil)
	r.Header.Set("Authorization", "Bearer secret-auth")
	r.Header.Set("Cookie", "session=secret-cookie")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["instance_id"] != a.instance || body["version"] != strings.TrimSpace(version) {
		t.Fatal(body)
	}
	if strings.Contains(w.Body.String(), "secret-") {
		t.Fatal("request secrets leaked")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("response can be cached")
	}
}

func TestReadinessLifecycle(t *testing.T) {
	a := newApp(time.Second)
	check := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("%s: got %d want %d", path, w.Code, want)
		}
	}
	check("/readyz", 503)
	check("/healthz", 200)
	a.started = time.Now().Add(-2 * time.Second)
	check("/readyz", 200)
	a.draining.Store(true)
	check("/readyz", 503)
	check("/healthz", 200)
}

func TestWaitBoundsAndCancellation(t *testing.T) {
	a := newApp(0)
	for _, duration := range []string{"-1s", "21s", "invalid"} {
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, httptest.NewRequest("GET", "/wait?duration="+duration, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", duration, w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		a.handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/wait?duration=20s", nil).WithContext(ctx))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled wait did not exit")
	}
	if len(a.waitSlots) != 0 {
		t.Fatal("wait slot leaked")
	}
}

