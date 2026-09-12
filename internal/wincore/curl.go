package wincore

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseCURL imports the supported one-request subset of curl without invoking a shell.
func ParseCURL(command string) (HTTPRequestSpec, error) {
	args, err := splitCurlCommand(command)
	if err != nil {
		return HTTPRequestSpec{}, err
	}
	if len(args) == 0 || args[0] != "curl" {
		return HTTPRequestSpec{}, fmt.Errorf("command must start with curl")
	}
	spec := HTTPRequestSpec{Method: http.MethodGet, Headers: make(http.Header)}
	methodSet, options := false, true
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if !options || !strings.HasPrefix(arg, "-") || arg == "-" {
			if spec.URL != "" {
				return HTTPRequestSpec{}, errorsf("multiple URLs are not supported")
			}
			spec.URL = arg
			continue
		}
		name, value, hasValue := curlOption(arg)
		need := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("curl option %s requires an argument", name)
			}
			i++
			return args[i], nil
		}
		switch name {
		case "-X", "--request":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Method, methodSet = strings.ToUpper(value), true
		case "-H", "--header":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			key, headerValue, ok := strings.Cut(value, ":")
			if !ok || !isHeaderName(key) {
				return HTTPRequestSpec{}, fmt.Errorf("invalid curl header %q", value)
			}
			spec.Headers.Add(strings.TrimSpace(key), strings.TrimSpace(headerValue))
		case "-d", "--data":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Data = append(spec.Data, HTTPDataPart{Kind: HTTPData, Value: value})
			if !methodSet {
				spec.Method = http.MethodPost
			}
		case "--data-raw":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Data = append(spec.Data, HTTPDataPart{Kind: HTTPDataRaw, Value: value})
			if !methodSet {
				spec.Method = http.MethodPost
			}
		case "--data-binary":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Data = append(spec.Data, HTTPDataPart{Kind: HTTPDataBinary, Value: value})
			if !methodSet {
				spec.Method = http.MethodPost
			}
		case "--data-urlencode":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Data = append(spec.Data, HTTPDataPart{Kind: HTTPDataEncode, Value: value})
			if !methodSet {
				spec.Method = http.MethodPost
			}
		case "-F", "--form":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			field, err := parseCurlForm(value)
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Form = append(spec.Form, field)
			if !methodSet {
				spec.Method = http.MethodPost
			}
		case "-b", "--cookie":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			if !strings.Contains(value, "=") {
				return HTTPRequestSpec{}, fmt.Errorf("cookie file %q is unsupported", value)
			}
			spec.Cookies = append(spec.Cookies, value)
		case "-u", "--user":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			username, password, _ := strings.Cut(value, ":")
			spec.Auth = &HTTPBasicAuth{Username: username, Password: password}
		case "-A", "--user-agent":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.UserAgent = value
		case "--connect-timeout":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.ConnectTimeout, err = curlSeconds(value)
			if err != nil {
				return HTTPRequestSpec{}, fmt.Errorf("invalid --connect-timeout: %w", err)
			}
		case "-m", "--max-time":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Timeout, err = curlSeconds(value)
			if err != nil {
				return HTTPRequestSpec{}, fmt.Errorf("invalid --max-time: %w", err)
			}
		case "-k", "--insecure":
			if hasValue {
				return HTTPRequestSpec{}, fmt.Errorf("curl option %s does not take an argument", name)
			}
			spec.Insecure = true
		case "-L", "--location":
			if hasValue {
				return HTTPRequestSpec{}, fmt.Errorf("curl option %s does not take an argument", name)
			}
			spec.FollowRedirects = true
		default:
			return HTTPRequestSpec{}, fmt.Errorf("unsupported curl option %s", name)
		}
	}
	if spec.URL == "" {
		return HTTPRequestSpec{}, errorsf("curl URL is required")
	}
	if len(spec.Data) > 0 && len(spec.Form) > 0 {
		return HTTPRequestSpec{}, fmt.Errorf("curl --data and --form cannot be combined")
	}
	return spec, nil
}

func errorsf(s string) error { return fmt.Errorf("%s", s) }

func curlOption(arg string) (name, value string, hasValue bool) {
	if name, value, hasValue = strings.Cut(arg, "="); hasValue {
		return
	}
	for _, short := range []string{"-X", "-H", "-d", "-F", "-b", "-u", "-A", "-m"} {
		if strings.HasPrefix(arg, short) && len(arg) > len(short) {
			return short, arg[len(short):], true
		}
	}
	return arg, "", false
}

func curlSeconds(s string) (time.Duration, error) {
	seconds, err := strconv.ParseFloat(s, 64)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("%q", s)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func parseCurlForm(value string) (HTTPFormField, error) {
	name, content, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return HTTPFormField{}, fmt.Errorf("invalid curl form %q", value)
	}
	if strings.HasPrefix(content, "@") {
		return HTTPFormField{Name: name, File: content[1:]}, nil
	}
	return HTTPFormField{Name: name, Value: content}, nil
}

func splitCurlCommand(command string) ([]string, error) {
	var args []string
	var current strings.Builder
	quote := byte(0)
	inToken := false
	flush := func() {
		if inToken {
			args = append(args, current.String())
			current.Reset()
			inToken = false
		}
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != '\'' && (c == '$' || c == '`') {
			return nil, fmt.Errorf("shell expansion is unsupported")
		}
		if quote == 0 {
			switch c {
			case ' ', '\t', '\r', '\n':
				flush()
			case '\'', '"':
				quote, inToken = c, true
			case '\\':
				inToken = true
				if i+1 >= len(command) {
					return nil, fmt.Errorf("unterminated escape")
				}
				i++
				if command[i] != '\n' {
					current.WriteByte(command[i])
				}
			case ';', '|', '&', '<', '>', '(', ')':
				return nil, fmt.Errorf("shell operator is unsupported")
			default:
				current.WriteByte(c)
				inToken = true
			}
			continue
		}
		if c == quote {
			quote = 0
			continue
		}
		if c == '\\' && quote == '"' {
			if i+1 >= len(command) {
				return nil, fmt.Errorf("unterminated escape")
			}
			i++
			if command[i] != '\n' {
				current.WriteByte(command[i])
			}
			continue
		}
		current.WriteByte(c)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return args, nil
}

// FormatCURL emits a safely single-quoted curl command for the supported request semantics.
func FormatCURL(spec HTTPRequestSpec) (string, error) {
	if spec.URL == "" {
		return "", fmt.Errorf("request URL is empty")
	}
	if len(spec.Data) > 0 && len(spec.Form) > 0 {
		return "", fmt.Errorf("request data and form cannot be combined")
	}
	parts := []string{"curl"}
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method != "" {
		parts = append(parts, "-X", curlQuote(method))
	}
	keys := make([]string, 0, len(spec.Headers))
	for key := range spec.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range spec.Headers.Values(key) {
			parts = append(parts, "-H", curlQuote(key+": "+value))
		}
	}
	for _, data := range spec.Data {
		flag := map[HTTPDataKind]string{HTTPData: "--data", HTTPDataRaw: "--data-raw", HTTPDataBinary: "--data-binary", HTTPDataEncode: "--data-urlencode"}[data.Kind]
		if flag == "" {
			return "", fmt.Errorf("unsupported data kind %q", data.Kind)
		}
		parts = append(parts, flag, curlQuote(data.Value))
	}
	if len(spec.Data) == 0 && len(spec.Body) > 0 {
		parts = append(parts, "--data-binary", curlQuote(string(spec.Body)))
	}
	for _, field := range spec.Form {
		if field.Name == "" {
			return "", fmt.Errorf("form field name is empty")
		}
		value := field.Name + "=" + field.Value
		if field.File != "" {
			value = field.Name + "=@" + field.File
		}
		parts = append(parts, "--form", curlQuote(value))
	}
	for _, cookie := range spec.Cookies {
		parts = append(parts, "--cookie", curlQuote(cookie))
	}
	if spec.Auth != nil {
		parts = append(parts, "--user", curlQuote(spec.Auth.Username+":"+spec.Auth.Password))
	}
	if spec.UserAgent != "" {
		parts = append(parts, "--user-agent", curlQuote(spec.UserAgent))
	}
	if spec.ConnectTimeout > 0 {
		parts = append(parts, "--connect-timeout", curlQuote(curlDuration(spec.ConnectTimeout)))
	}
	if spec.Timeout > 0 {
		parts = append(parts, "--max-time", curlQuote(curlDuration(spec.Timeout)))
	}
	if spec.Insecure {
		parts = append(parts, "--insecure")
	}
	if spec.FollowRedirects {
		parts = append(parts, "--location")
	}
	return strings.Join(append(parts, curlQuote(spec.URL)), " "), nil
}

func curlDuration(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) }

func curlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
