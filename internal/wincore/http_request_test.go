package wincore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newHTTPTestEngine(t *testing.T, address string) *Engine {
	t.Helper()
	e, err := New(t.TempDir(), func(string, []byte) {}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if err := e.Connect(Config{Mode: ModeHTTPClient, Address: address}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDoHTTPRequestUsesDataBodyAndCookieJar(t *testing.T) {
	var sawPOST atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || string(body) != "user=me&pass=secret" {
			http.Error(w, "bad request", 400)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/private", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err == nil && c.Value == "ok" {
			sawPOST.Store(true)
			_, _ = w.Write([]byte("private"))
			return
		}
		http.Error(w, "missing cookie", http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e := newHTTPTestEngine(t, srv.URL)

	login, err := e.DoHTTPRequest(context.Background(), HTTPRequestSpec{Method: "POST", URL: srv.URL + "/login", Data: []HTTPDataPart{{Kind: HTTPData, Value: "user=me"}, {Kind: HTTPData, Value: "pass=secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if login.StatusCode != 200 || string(login.RawBody) != `{"ok":true}` || !strings.Contains(string(login.PrettyBody), "\n") || login.ByteSize != int64(len(login.RawBody)) || login.Duration < 0 {
		t.Fatalf("login result = %#v", login)
	}
	private, err := e.DoHTTPRequest(context.Background(), HTTPRequestSpec{URL: srv.URL + "/private"})
	if err != nil {
		t.Fatal(err)
	}
	if private.StatusCode != 200 || string(private.RawBody) != "private" || !sawPOST.Load() {
		t.Fatalf("jar was not reused: %#v", private)
	}
}

func TestDoHTTPRequestHonorsRedirectAndExplicitHost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/done", http.StatusFound) })
	mux.HandleFunc("/done", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "wanted.test" {
			http.Error(w, r.Host, 400)
			return
		}
		_, _ = w.Write([]byte("done"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	e := newHTTPTestEngine(t, srv.URL)

	noFollow, err := e.DoHTTPRequest(context.Background(), HTTPRequestSpec{URL: srv.URL + "/start"})
	if err != nil || noFollow.StatusCode != http.StatusFound {
		t.Fatalf("no redirect = %#v, %v", noFollow, err)
	}
	follow, err := e.DoHTTPRequest(context.Background(), HTTPRequestSpec{URL: srv.URL + "/start", FollowRedirects: true, Headers: http.Header{"Host": {"wanted.test"}}})
	if err != nil || follow.StatusCode != 200 || string(follow.RawBody) != "done" {
		t.Fatalf("redirect/host = %#v, %v", follow, err)
	}
}

func TestDoHTTPRequestCancelsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	e := newHTTPTestEngine(t, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := e.DoHTTPRequest(ctx, HTTPRequestSpec{URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestDoHTTPRequestPreservesCurlJSONAndBinaryDataSemantics(t *testing.T) {
	file := t.TempDir() + "/payload.bin"
	if err := os.WriteFile(file, []byte{'a', 0, 'b', '\n'}, 0o600); err != nil {
		t.Fatal(err)
	}
	var got [][]byte
	var types []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, body)
		types = append(types, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	e := newHTTPTestEngine(t, srv.URL)
	jsonSpec, err := ParseCURL("curl --data '{\"ok\":true}' " + srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DoHTTPRequest(context.Background(), jsonSpec); err != nil {
		t.Fatal(err)
	}
	binarySpec, err := ParseCURL("curl --data-binary @" + shellQuote(file) + " " + srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DoHTTPRequest(context.Background(), binarySpec); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got[0]) != `{"ok":true}` || types[0] != "application/x-www-form-urlencoded" || string(got[1]) != "a\x00b\n" || types[1] != "application/x-www-form-urlencoded" {
		t.Fatalf("bodies/types = %q / %q", got, types)
	}
}

func TestDoHTTPRequestSendsMultipartAndBasicAuth(t *testing.T) {
	file := t.TempDir() + "/upload.txt"
	if err := os.WriteFile(file, []byte("file body"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.FormValue("name") != "commbox" {
			http.Error(w, "bad form", 400)
			return
		}
		f, _, err := r.FormFile("upload")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		user, password, ok := r.BasicAuth()
		if string(data) != "file body" || !ok || user != "me" || password != "pw" {
			http.Error(w, "bad upload", 400)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	e := newHTTPTestEngine(t, srv.URL)
	spec, err := ParseCURL("curl -u me:pw -F name=commbox -F upload=@" + shellQuote(file) + " " + srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.DoHTTPRequest(context.Background(), spec)
	if err != nil || result.StatusCode != http.StatusCreated {
		t.Fatalf("multipart result = %#v, %v", result, err)
	}
}

func TestDoHTTPRequestHonorsTimeoutAndInsecureTLS(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(200 * time.Millisecond) }))
	defer slow.Close()
	e := newHTTPTestEngine(t, slow.URL)
	if _, err := e.DoHTTPRequest(context.Background(), HTTPRequestSpec{URL: slow.URL, Timeout: 20 * time.Millisecond}); err == nil {
		t.Fatal("max time did not stop request")
	}

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("secure")) }))
	defer tlsServer.Close()
	if _, err := e.DoHTTPRequest(context.Background(), HTTPRequestSpec{URL: tlsServer.URL}); err == nil {
		t.Fatal("untrusted TLS unexpectedly succeeded")
	}
	result, err := e.DoHTTPRequest(context.Background(), HTTPRequestSpec{URL: tlsServer.URL, Insecure: true})
	if err != nil || string(result.RawBody) != "secure" {
		t.Fatalf("insecure TLS result = %#v, %v", result, err)
	}
}
