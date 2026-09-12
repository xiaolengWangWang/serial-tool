package wincore

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HTTPDataKind records the cURL data option that supplied a body part.
type HTTPDataKind string

const (
	HTTPData       HTTPDataKind = "data"
	HTTPDataRaw    HTTPDataKind = "data-raw"
	HTTPDataBinary HTTPDataKind = "data-binary"
	HTTPDataEncode HTTPDataKind = "data-urlencode"
)

type HTTPDataPart struct {
	Kind  HTTPDataKind
	Value string // Kept verbatim, including @file, so previews do not hide file references.
}

type HTTPFormField struct {
	Name  string
	Value string
	File  string
}

type HTTPBasicAuth struct {
	Username string
	Password string
}

// HTTPRequestSpec is an executable HTTP request without shell interpretation.
type HTTPRequestSpec struct {
	Method          string
	URL             string
	Headers         http.Header
	Body            []byte
	Data            []HTTPDataPart
	Form            []HTTPFormField
	Auth            *HTTPBasicAuth
	Cookies         []string
	UserAgent       string
	ConnectTimeout  time.Duration
	Timeout         time.Duration
	FollowRedirects bool
	Insecure        bool
}

// HTTPResponseResult contains the received response. RawBody is never reformatted.
type HTTPResponseResult struct {
	StatusCode int
	Status     string
	Headers    http.Header
	RawBody    []byte
	PrettyBody []byte
	Duration   time.Duration
	ByteSize   int64
	URL        string
}

func (s HTTPRequestSpec) requestBody() ([]byte, error) {
	body, _, err := s.buildBody()
	return body, err
}

func (s HTTPRequestSpec) buildBody() ([]byte, string, error) {
	if len(s.Data) > 0 && len(s.Form) > 0 {
		return nil, "", errors.New("request data and form cannot be combined")
	}
	if (len(s.Data) > 0 || len(s.Form) > 0) && len(s.Body) > 0 {
		return nil, "", errors.New("request body cannot be combined with data or form")
	}
	if len(s.Form) > 0 {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for _, field := range s.Form {
			if field.Name == "" {
				return nil, "", errors.New("form field name is empty")
			}
			if field.File == "" {
				if err := writer.WriteField(field.Name, field.Value); err != nil {
					return nil, "", err
				}
				continue
			}
			file, err := os.Open(field.File)
			if err != nil {
				return nil, "", fmt.Errorf("read form file %q: %w", field.File, err)
			}
			part, err := writer.CreateFormFile(field.Name, fileName(field.File))
			if err == nil {
				_, err = io.Copy(part, file)
			}
			closeErr := file.Close()
			if err != nil {
				return nil, "", err
			}
			if closeErr != nil {
				return nil, "", closeErr
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", err
		}
		return body.Bytes(), writer.FormDataContentType(), nil
	}
	if len(s.Data) == 0 {
		return append([]byte(nil), s.Body...), "", nil
	}
	parts := make([][]byte, 0, len(s.Data))
	for _, part := range s.Data {
		data, err := dataPartBytes(part)
		if err != nil {
			return nil, "", err
		}
		parts = append(parts, data)
	}
	return bytes.Join(parts, []byte("&")), "application/x-www-form-urlencoded", nil
}

func fileName(path string) string {
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func dataPartBytes(part HTTPDataPart) ([]byte, error) {
	value := part.Value
	switch part.Kind {
	case HTTPData:
		if strings.HasPrefix(value, "@") {
			data, err := os.ReadFile(value[1:])
			if err != nil {
				return nil, err
			}
			stripped := data[:0]
			for _, b := range data {
				if b != '\r' && b != '\n' && b != 0 {
					stripped = append(stripped, b)
				}
			}
			return stripped, nil
		}
		return []byte(value), nil
	case HTTPDataRaw:
		return []byte(value), nil
	case HTTPDataBinary:
		if strings.HasPrefix(value, "@") {
			return os.ReadFile(value[1:])
		}
		return []byte(value), nil
	case HTTPDataEncode:
		return urlEncodeData(value)
	default:
		return nil, fmt.Errorf("unsupported data kind %q", part.Kind)
	}
}

func urlEncodeData(value string) ([]byte, error) {
	name, content := "", value
	if i := strings.IndexByte(value, '='); i >= 0 {
		name, content = value[:i], value[i+1:]
	} else if i := strings.IndexByte(value, '@'); i >= 0 {
		name, content = value[:i], value[i:]
	}
	if strings.HasPrefix(content, "@") {
		data, err := os.ReadFile(content[1:])
		if err != nil {
			return nil, err
		}
		content = string(data)
	}
	encoded := queryEscape(content)
	if name == "" && strings.HasPrefix(value, "=") {
		return []byte(encoded), nil
	}
	if name != "" {
		return []byte(name + "=" + encoded), nil
	}
	return []byte(encoded), nil
}

func queryEscape(s string) string {
	return url.QueryEscape(s)
}

// DoHTTPRequest executes a structured request and reuses the connected HTTP client's cookie jar.
func (e *Engine) DoHTTPRequest(ctx context.Context, spec HTTPRequestSpec) (HTTPResponseResult, error) {
	e.Lock()
	base, original := e.httpURL, e.httpClient
	e.Unlock()
	if original == nil {
		return HTTPResponseResult{}, errors.New("尚未连接 HTTP")
	}
	if spec.URL == "" {
		spec.URL = base
	}
	if spec.URL == "" {
		return HTTPResponseResult{}, errors.New("request URL is empty")
	}
	body, defaultContentType, err := spec.buildBody()
	if err != nil {
		return HTTPResponseResult{}, err
	}
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
		if len(spec.Data) > 0 || len(spec.Form) > 0 || len(spec.Body) > 0 {
			method = http.MethodPost
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, spec.URL, bytes.NewReader(body))
	if err != nil {
		return HTTPResponseResult{}, err
	}
	for key, values := range spec.Headers {
		if strings.EqualFold(key, "Host") {
			if len(values) > 0 {
				req.Host = values[0]
			}
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if defaultContentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", defaultContentType)
	}
	if spec.UserAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", spec.UserAgent)
	}
	if len(spec.Cookies) > 0 && req.Header.Get("Cookie") == "" {
		req.Header.Set("Cookie", strings.Join(spec.Cookies, "; "))
	}
	if spec.Auth != nil && req.Header.Get("Authorization") == "" {
		req.SetBasicAuth(spec.Auth.Username, spec.Auth.Password)
	}

	client := *original // preserves its Jar while allowing request-specific transport and redirect settings.
	if spec.Timeout > 0 {
		client.Timeout = spec.Timeout
	}
	if spec.FollowRedirects {
		client.CheckRedirect = nil
	} else {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	if spec.ConnectTimeout > 0 || spec.Insecure {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if current, ok := original.Transport.(*http.Transport); ok && current != nil {
			transport = current.Clone()
		}
		if spec.ConnectTimeout > 0 {
			transport.DialContext = (&net.Dialer{Timeout: spec.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
		}
		if spec.Insecure {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			} else {
				transport.TLSClientConfig = transport.TLSClientConfig.Clone()
			}
			transport.TLSClientConfig.InsecureSkipVerify = true // user explicitly requested curl -k semantics.
		}
		client.Transport = transport
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return HTTPResponseResult{}, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return HTTPResponseResult{}, readErr
	}
	return HTTPResponseResult{StatusCode: resp.StatusCode, Status: resp.Status, Headers: resp.Header.Clone(), RawBody: raw, PrettyBody: prettyJSON(resp.Header.Get("Content-Type"), raw), Duration: time.Since(start), ByteSize: int64(len(raw)), URL: resp.Request.URL.String()}, nil
}
