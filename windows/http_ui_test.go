//go:build windows

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"serial-tool/internal/wincore"
	"strings"
	"testing"
	"time"
)

func TestHTTPWorkspaceMapsFieldsAndPreservesCookies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cookie" {
			if c, e := r.Cookie("session"); e != nil || c.Value != "ok" {
				http.Error(w, "missing cookie", 401)
				return
			}
			w.Write([]byte("cookie retained"))
			return
		}
		b, _ := io.ReadAll(r.Body)
		if r.Method != "PATCH" || string(b) != "payload" || len(r.Header.Values("X-Test")) != 2 {
			http.Error(w, "bad mapping", 400)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.WriteHeader(201)
		w.Write([]byte("created"))
	}))
	defer srv.Close()
	e, err := wincore.New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = e.Connect(wincore.Config{Mode: wincore.ModeHTTPClient, Address: srv.URL}); err != nil {
		t.Fatal(err)
	}
	f := httpWorkspaceFields{method: " patch ", url: srv.URL, headers: "X-Test: one\r\nX-Test: two", body: "payload", timeout: "2", connectTimeout: "1"}
	result, err := httpWorkspaceRequest(context.Background(), e, f)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 201 || result.ByteSize != 7 {
		t.Fatalf("response: %+v", result)
	}
	f.method = "GET"
	f.url = srv.URL + "/cookie"
	f.body = ""
	result, err = httpWorkspaceRequest(context.Background(), e, f)
	if err != nil || result.StatusCode != 200 || string(result.RawBody) != "cookie retained" {
		t.Fatalf("cookie request: %+v %v", result, err)
	}
}

func TestHTTPWorkspaceCancellationReachesServer(t *testing.T) {
	entered := make(chan struct{})
	stopped := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(stopped) }))
	defer srv.Close()
	e, err := wincore.New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = e.Connect(wincore.Config{Mode: wincore.ModeHTTPClient, Address: srv.URL}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := httpWorkspaceRequest(ctx, e, httpWorkspaceFields{method: "GET", url: srv.URL, timeout: "5", connectTimeout: "1"})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request never arrived")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel blocked")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("server request not cancelled")
	}
}

func TestHTTPWorkspacePreservesImportedBodyAndRejectsInvalidFields(t *testing.T) {
	base, err := wincore.ParseCURL("curl -u 'user:pass' --data-urlencode 'a=x y' 'http://127.0.0.1/'")
	if err != nil {
		t.Fatal(err)
	}
	f := httpWorkspaceFields{base: base, preserveBody: true, method: "POST", url: base.URL, timeout: "3", connectTimeout: "1", follow: true, insecure: true}
	spec, err := f.spec()
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Data) != 1 || spec.Auth == nil || spec.Timeout != 3*time.Second || !spec.FollowRedirects || !spec.Insecure {
		t.Fatal("import options lost")
	}
	f.preserveBody = false
	f.body = "replacement"
	spec, err = f.spec()
	if err != nil || len(spec.Data) != 0 || string(spec.Body) != "replacement" {
		t.Fatal("body replacement failed")
	}
	for _, bad := range []string{"-1", "NaN", "Inf", "garbage", "1e99"} {
		f.timeout = bad
		if _, err = f.spec(); err == nil {
			t.Fatalf("accepted timeout %q", bad)
		}
	}
	f.timeout = "3"
	f.headers = "invalid header"
	if _, err = f.spec(); err == nil {
		t.Fatal("accepted malformed header")
	}
	f.headers = ""
	f.url = "file:///private"
	if _, err = f.spec(); err == nil {
		t.Fatal("accepted non-HTTP URL")
	}
}

func TestHTTPWorkspaceDisplayBoundsUnicode(t *testing.T) {
	got := httpWorkspaceBounded(strings.Repeat("界", 200000))
	if len(got) > 600000 || !strings.Contains(got, "截断") {
		t.Fatal("large output not bounded")
	}
	if got := httpWorkspaceBounded("small"); got != "small" {
		t.Fatal("small output changed")
	}
}
