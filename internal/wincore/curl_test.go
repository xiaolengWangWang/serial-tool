package wincore

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCURLPreservesQuotedDataAndSelectedOptions(t *testing.T) {
	spec, err := ParseCURL("curl -X POST -H 'X-Trace: a b' --data-raw 'line 1\n$literal; & ' -b 'sid=abc' -u 'me:pw' -A 'CommBox test' --connect-timeout 0.25 -m 2 -k -L 'https://example.test/api?q=a%20b'")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Method != http.MethodPost || spec.URL != "https://example.test/api?q=a%20b" || spec.Headers.Get("X-Trace") != "a b" {
		t.Fatalf("parsed request = %#v", spec)
	}
	if len(spec.Data) != 1 || spec.Data[0].Kind != HTTPDataRaw || spec.Data[0].Value != "line 1\n$literal; & " {
		t.Fatalf("data = %#v", spec.Data)
	}
	if len(spec.Cookies) != 1 || spec.Cookies[0] != "sid=abc" || spec.Auth == nil || spec.Auth.Username != "me" || spec.Auth.Password != "pw" || spec.UserAgent != "CommBox test" {
		t.Fatalf("credentials = %#v", spec)
	}
	if spec.ConnectTimeout != 250*time.Millisecond || spec.Timeout != 2*time.Second || !spec.Insecure || !spec.FollowRedirects {
		t.Fatalf("options = %#v", spec)
	}

	formatted, err := FormatCURL(spec)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := ParseCURL(formatted)
	if err != nil {
		t.Fatalf("formatted curl did not parse: %q: %v", formatted, err)
	}
	if roundTrip.Method != spec.Method || roundTrip.URL != spec.URL || len(roundTrip.Data) != 1 || roundTrip.Data[0] != spec.Data[0] || !roundTrip.FollowRedirects || !roundTrip.Insecure {
		t.Fatalf("round trip = %#v\nfrom %s", roundTrip, formatted)
	}
}

func TestParseCURLDataFileSemanticsAndPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, []byte("a\r\nb\x00c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := ParseCURL("curl --data @" + shellQuote(path) + " --data-binary @" + shellQuote(path) + " --data-raw @literal https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Data) != 3 || spec.Data[0].Value != "@"+path || spec.Data[1].Value != "@"+path || spec.Data[2].Value != "@literal" {
		t.Fatalf("file references were not retained: %#v", spec.Data)
	}
	body, err := spec.requestBody()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("abc&a\r\nb\x00c\n&@literal")
	if !bytes.Equal(body, want) {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestParseCURLDataFileKeepsNonNewlineBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte{0xff, 0, '\r', 'x', '\n', 0x80}, 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := ParseCURL("curl --data @" + shellQuote(path) + " https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	body, err := spec.requestBody()
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xff, 'x', 0x80}; !bytes.Equal(body, want) {
		t.Fatalf("data file = %x, want %x", body, want)
	}
}

func TestParseCURLURLencodeAndForm(t *testing.T) {
	spec, err := ParseCURL("curl --data-urlencode 'a=b c' --data-urlencode '=hello world' https://example.test/upload")
	if err != nil {
		t.Fatal(err)
	}
	body, err := spec.requestBody()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "a=b+c&hello+world"; got != want {
		t.Fatalf("urlencoded body = %q, want %q", got, want)
	}
	formSpec, err := ParseCURL("curl -F 'title=hello world' -F 'upload=@/tmp/x.bin' https://example.test/upload")
	if err != nil {
		t.Fatal(err)
	}
	if len(formSpec.Form) != 2 || formSpec.Form[0].Name != "title" || formSpec.Form[0].Value != "hello world" || formSpec.Form[1].File != "/tmp/x.bin" {
		t.Fatalf("form = %#v", formSpec.Form)
	}
}

func TestParseCURLRejectsUnsupportedAndShellSyntax(t *testing.T) {
	for _, command := range []string{
		"curl --compressed https://example.test",
		"curl -d a=1 -F b=2 https://example.test",
		"curl https://example.test | sh",
		"curl https://example.test/$TOKEN",
		"wget https://example.test",
	} {
		if _, err := ParseCURL(command); err == nil {
			t.Errorf("ParseCURL(%q) succeeded", command)
		}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func TestFormatCURLSafelyQuotesShellMetacharacters(t *testing.T) {
	formatted, err := FormatCURL(HTTPRequestSpec{Method: "POST", URL: "https://example.test/a?x=$not-expanded", Data: []HTTPDataPart{{Kind: HTTPDataBinary, Value: "$(literal); &"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formatted, "'$") || strings.Contains(formatted, "$(literal); &"+" ") {
		t.Fatalf("unsafe curl: %s", formatted)
	}
	if _, err := ParseCURL(formatted); err != nil {
		t.Fatalf("format output rejected: %v", err)
	}
}

func TestRedactHTTPRequestMasksNestedCredentials(t *testing.T) {
	spec := HTTPRequestSpec{
		URL:     "https://example.test/api?token=abc&safe=yes",
		Headers: http.Header{"Authorization": {"Bearer abc"}, "X-Api-Key": {"key"}, "X-Trace": {"ok"}},
		Body:    []byte(`{"outer":{"Password":"secret","safe":"ok"},"token":"abc"}`),
		Cookies: []string{"sid=abc"}, Auth: &HTTPBasicAuth{Username: "me", Password: "pw"},
	}
	redacted := RedactHTTPRequest(spec)
	u, err := url.Parse(redacted.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("token") != redactedValue || u.Query().Get("safe") != "yes" || redacted.Headers.Get("Authorization") != redactedValue || redacted.Headers.Get("X-Api-Key") != redactedValue || redacted.Headers.Get("X-Trace") != "ok" {
		t.Fatalf("headers/query = %#v %s", redacted.Headers, redacted.URL)
	}
	if strings.Contains(string(redacted.Body), "secret") || strings.Contains(string(redacted.Body), "\"abc\"") || redacted.Auth.Password != redactedValue || redacted.Cookies[0] != redactedValue {
		t.Fatalf("redacted request leaked: %#v", redacted)
	}
}
