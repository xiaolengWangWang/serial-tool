package wincore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHTTPClientMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/data" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"10001":78.5}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	var mu sync.Mutex
	var got string
	done := make(chan struct{}, 1)
	engine, err := New(t.TempDir(), func(_ string, data []byte) {
		mu.Lock()
		got = string(data)
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	if err := engine.Connect(Config{Mode: ModeHTTPClient, Address: server.URL + "/api/data"}); err != nil {
		t.Fatalf("HTTP 连接失败: %v", err)
	}
	if err := engine.Send("", false, "无"); err != nil {
		t.Fatalf("HTTP GET 失败: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 HTTP 响应")
	}
	mu.Lock()
	ok := strings.Contains(got, "200 OK") && strings.Contains(got, "10001") && strings.Contains(got, "78.5")
	body := got
	got = ""
	mu.Unlock()
	if !ok {
		t.Fatalf("HTTP 响应内容不符: %q", body)
	}

	// path override:发送框填相对路径应命中 404
	if err := engine.Send("/nope", false, "无"); err != nil {
		t.Fatalf("HTTP GET(override) 失败: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 override 响应")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(got, "404") {
		t.Fatalf("路径覆盖未生效: %q", got)
	}
}

// TestHTTPLoginCookie 验证:POST 登录拿到 Cookie 后,后续 GET 自动带上会话 Cookie。
func TestHTTPLoginCookie(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Method == http.MethodPost && r.FormValue("username") == "admin" && r.FormValue("password") == "secret" {
			http.SetCookie(w, &http.Cookie{Name: "sess", Value: "ok", Path: "/"})
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "bad", http.StatusUnauthorized)
	})
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("sess"); err != nil || c.Value != "ok" {
			http.Error(w, `{"error":"login_required"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var mu sync.Mutex
	var got string
	done := make(chan struct{}, 1)
	engine, err := New(t.TempDir(), func(_ string, data []byte) {
		mu.Lock()
		got = string(data)
		mu.Unlock()
		done <- struct{}{}
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	if err := engine.Connect(Config{Mode: ModeHTTPClient, Address: server.URL}); err != nil {
		t.Fatal(err)
	}
	wait := func(label string) string {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 超时", label)
		}
		mu.Lock()
		defer mu.Unlock()
		return got
	}

	// 未登录直接访问 → 401
	if err := engine.Send("GET /api/v1/health", false, "无"); err != nil {
		t.Fatal(err)
	}
	if r := wait("未登录访问"); !strings.Contains(r, "401") {
		t.Fatalf("未登录应 401,实际 %q", r)
	}

	// 登录 → 拿到 Cookie
	if err := engine.Send("POST /login\nusername=admin&password=secret", false, "无"); err != nil {
		t.Fatal(err)
	}
	if r := wait("登录"); !strings.Contains(r, "200 OK") {
		t.Fatalf("登录应 200,实际 %q", r)
	}

	// 带会话再访问 → 200 + 数据
	if err := engine.Send("GET /api/v1/health", false, "无"); err != nil {
		t.Fatal(err)
	}
	if r := wait("已登录访问"); !strings.Contains(r, "200 OK") || !strings.Contains(r, `"ok"`) || !strings.Contains(r, "true") {
		t.Fatalf("已登录应返回数据,实际 %q", r)
	}
}
