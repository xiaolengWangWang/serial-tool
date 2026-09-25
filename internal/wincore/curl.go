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
	methodSet, options, jsonBody := false, true, false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		// 组合短选项(-sSL、-sXPOST)在这里逐个拆,只拆处在选项位置的参数:
		// 被 need() 取走的选项值(-d '-sort=asc'、--user '-L:')不会走到这里。
		if options && len(arg) > 2 && arg[0] == '-' && arg[1] != '-' && strings.IndexByte(curlShortNoArg, arg[1]) >= 0 {
			args[i] = "-" + arg[2:] // 余下部分下一轮再处理
			i--
			arg = arg[:2]
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
		case "--json":
			value, err = need()
			if err != nil {
				return HTTPRequestSpec{}, err
			}
			spec.Data = append(spec.Data, HTTPDataPart{Kind: HTTPDataBinary, Value: value})
			jsonBody = true
			if !methodSet {
				spec.Method = http.MethodPost
			}
		case "--compressed", "-s", "--silent", "-S", "--show-error", "-i", "--include", "-v", "--verbose":
			// 只影响 curl 自身的输出或解压;Go 客户端默认就会透明解压 gzip。
			if hasValue {
				return HTTPRequestSpec{}, fmt.Errorf("curl option %s does not take an argument", name)
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
	if jsonBody {
		// 与 curl 一致:--json 补默认的 JSON 类型头,显式 -H 优先。
		if spec.Headers.Get("Content-Type") == "" {
			spec.Headers.Set("Content-Type", "application/json")
		}
		if spec.Headers.Get("Accept") == "" {
			spec.Headers.Set("Accept", "application/json")
		}
	}
	return spec, nil
}

func errorsf(s string) error { return fmt.Errorf("%s", s) }

// curlShortWithArg 是带参数的短选项,值可以连写(-XPOST、-d'a=b')。
var curlShortWithArg = []string{"-X", "-H", "-d", "-F", "-b", "-u", "-A", "-m"}

// curlShortNoArg 是可以组合写的无参数短选项字母(-sSL、-sk)。
const curlShortNoArg = "sSivLk"

// curlOption 拆出选项名和连写的值。长选项按 "=" 拆;短选项的连写值里
// 可能本身含 "="(-d'a=b'、-H'X: a=b'),所以短选项先按前缀识别。
func curlOption(arg string) (name, value string, hasValue bool) {
	if strings.HasPrefix(arg, "--") {
		return strings.Cut(arg, "=")
	}
	for _, short := range curlShortWithArg {
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
		// Chrome「复制为 cURL (bash)」遇到换行、引号等会写成 $'...'(ANSI-C 引号)。
		// 这只是字面量写法,不是变量展开,按 bash 规则还原转义即可。
		if quote == 0 && c == '$' && i+1 < len(command) && command[i+1] == '\'' {
			value, next, err := ansiCQuoted(command, i+2)
			if err != nil {
				return nil, err
			}
			current.WriteString(value)
			inToken = true
			i = next
			continue
		}
		if quote != '\'' && (c == '$' || c == '`') {
			return nil, fmt.Errorf("shell expansion is unsupported")
		}
		// A continued line joins the command; it does not start an empty argument.
		// Accept Windows CRLF from text controls without rewriting quoted body bytes.
		if quote != '\'' && c == '\\' && i+1 < len(command) {
			if command[i+1] == '\n' {
				i++
				continue
			}
			if command[i+1] == '\r' && i+2 < len(command) && command[i+2] == '\n' {
				i += 2
				continue
			}
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

// ansiCQuoted 解析 $'...' 的内容,start 指向开头引号之后。返回还原后的值和
// 结尾引号的下标。转义规则同 bash:\a \b \e \f \n \r \t \v \\ \' \" \?、
// \xHH、\uHHHH、\UHHHHHHHH、\NNN(八进制)、\cX;未知转义保留反斜杠。
func ansiCQuoted(command string, start int) (string, int, error) {
	var b strings.Builder
	for i := start; i < len(command); i++ {
		c := command[i]
		if c == '\'' {
			return b.String(), i, nil
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(command) {
			break
		}
		i++
		e := command[i]
		switch e {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'e', 'E':
			b.WriteByte(0x1b)
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '\\', '\'', '"', '?':
			b.WriteByte(e)
		case 'c':
			if i+1 < len(command) {
				i++
				b.WriteByte(command[i] & 0x1f)
			}
		case 'x', 'u', 'U':
			max := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
			j := i + 1
			for j < len(command) && j-i-1 < max && isHexDigit(command[j]) {
				j++
			}
			if j == i+1 {
				b.WriteByte('\\')
				b.WriteByte(e)
				continue
			}
			n, _ := strconv.ParseUint(command[i+1:j], 16, 32)
			if e == 'x' {
				b.WriteByte(byte(n))
			} else {
				b.WriteRune(rune(n))
			}
			i = j - 1
		default:
			if e >= '0' && e <= '7' {
				j := i
				for j < len(command) && j-i < 3 && command[j] >= '0' && command[j] <= '7' {
					j++
				}
				n, _ := strconv.ParseUint(command[i:j], 8, 16)
				b.WriteByte(byte(n))
				i = j - 1
				continue
			}
			b.WriteByte('\\')
			b.WriteByte(e)
		}
	}
	return "", 0, fmt.Errorf("unterminated $'...' quote")
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
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
	if strings.HasPrefix(spec.URL, "-") {
		parts = append(parts, "--") // 否则导入时会把 URL 当成选项
	}
	return strings.Join(append(parts, curlQuote(spec.URL)), " "), nil
}

func curlDuration(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) }

func curlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
