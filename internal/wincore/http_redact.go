package wincore

import (
	"encoding/json"
	"net/url"
	"strings"
)

const redactedValue = "[REDACTED]"

// RedactHTTPRequest returns a safe copy for previews; it never alters the executable request.
func RedactHTTPRequest(spec HTTPRequestSpec) HTTPRequestSpec {
	redacted := spec
	redacted.Headers = spec.Headers.Clone()
	for key := range redacted.Headers {
		if sensitiveHTTPKey(key) {
			redacted.Headers[key] = []string{redactedValue}
		}
	}
	if u, err := url.Parse(spec.URL); err == nil {
		query := u.Query()
		for key := range query {
			if sensitiveHTTPKey(key) {
				query[key] = []string{redactedValue}
			}
		}
		u.RawQuery = query.Encode()
		if u.User != nil {
			username := u.User.Username()
			u.User = url.UserPassword(username, redactedValue)
		}
		redacted.URL = u.String()
	}
	redacted.Body = redactHTTPBody(spec.Body)
	redacted.Data = append([]HTTPDataPart(nil), spec.Data...)
	for i := range redacted.Data {
		redacted.Data[i].Value = redactDataValue(redacted.Data[i].Value)
	}
	redacted.Form = append([]HTTPFormField(nil), spec.Form...)
	for i := range redacted.Form {
		if sensitiveHTTPKey(redacted.Form[i].Name) {
			redacted.Form[i].Value = redactedValue
		}
	}
	redacted.Cookies = make([]string, len(spec.Cookies))
	for i := range redacted.Cookies {
		redacted.Cookies[i] = redactedValue
	}
	if spec.Auth != nil {
		redacted.Auth = &HTTPBasicAuth{Username: spec.Auth.Username, Password: redactedValue}
	}
	return redacted
}

// RedactHTTPResponse returns a safe response preview using the same header/body rules.
func RedactHTTPResponse(result HTTPResponseResult) HTTPResponseResult {
	redacted := result
	redacted.Headers = result.Headers.Clone()
	for key := range redacted.Headers {
		if sensitiveHTTPKey(key) {
			redacted.Headers[key] = []string{redactedValue}
		}
	}
	redacted.RawBody = redactHTTPBody(result.RawBody)
	redacted.PrettyBody = prettyJSON(redacted.Headers.Get("Content-Type"), redacted.RawBody)
	return redacted
}

func sensitiveHTTPKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "_", "-"))
	return key == "authorization" || key == "proxy-authorization" || key == "cookie" || key == "set-cookie" ||
		strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "api-key") || strings.Contains(key, "apikey")
}

func redactDataValue(value string) string {
	if strings.HasPrefix(value, "@") {
		return value
	}
	return string(redactHTTPBody([]byte(value)))
}

func redactHTTPBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(body, &value) == nil {
		redactJSONValue(value)
		if out, err := json.Marshal(value); err == nil {
			return out
		}
	}
	form, err := url.ParseQuery(string(body))
	if err == nil && len(form) > 0 {
		changed := false
		for key := range form {
			if sensitiveHTTPKey(key) {
				form[key] = []string{redactedValue}
				changed = true
			}
		}
		if changed {
			return []byte(form.Encode())
		}
	}
	return append([]byte(nil), body...)
}

func redactJSONValue(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if sensitiveHTTPKey(key) {
				v[key] = redactedValue
			} else {
				redactJSONValue(child)
			}
		}
	case []any:
		for _, child := range v {
			redactJSONValue(child)
		}
	}
}
