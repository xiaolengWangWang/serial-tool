package wincore

// 解析类函数的模糊测试。平时 go test 只跑种子;深挖用
//   go test -run '^$' -fuzz '^FuzzCURLRoundTrip$' -fuzztime 2m ./internal/wincore/

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzAnalyze(f *testing.F) {
	for _, s := range []string{"01 03 00 00 00 02 C4 0B", "00 01 00 00 00 06 01 03 00 00 00 01", "45 00 00 1c 00 00 00 00 40 11 00 00 7f 00 00 01 7f 00 00 01 00 35 00 35 00 08 00 00", "", "ff"} {
		f.Add("TCP", s)
		f.Add("", s)
	}
	f.Fuzz(func(t *testing.T, transport, in string) {
		_ = AnalyzeTransportPacket(transport, in)
	})
}

func FuzzCURLRoundTrip(f *testing.F) {
	for _, s := range []string{`curl -sSL -X POST -H 'A: b' -d'a=b' --json '{}' https://x/`, `curl -F 'f=@/tmp/x' https://x`, `curl -u a:b -k -L --max-time 3 http://x`,
		`curl -u-L 0`, `curl -d '-sort=asc' https://x/`, `curl -- -0`} { // 以 - 开头的选项值、以 - 开头的 URL
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		spec, err := ParseCURL(cmd)
		if err != nil {
			return
		}
		out, err := FormatCURL(spec)
		if err != nil {
			return
		}
		again, err := ParseCURL(out)
		if err != nil {
			t.Fatalf("FormatCURL output rejected\nin:  %q\nout: %q\nerr: %v", cmd, out, err)
		}
		if normMethod(again) != normMethod(spec) || again.URL != spec.URL || len(again.Data) != len(spec.Data) || len(again.Form) != len(spec.Form) || again.Insecure != spec.Insecure || again.FollowRedirects != spec.FollowRedirects {
			t.Fatalf("round trip changed request\nin:  %q\nout: %q\n%#v\n%#v", cmd, out, spec, again)
		}
		for i := range spec.Data {
			if spec.Data[i] != again.Data[i] {
				t.Fatalf("data changed\nin:  %q\nout: %q\n%#v\n%#v", cmd, out, spec.Data, again.Data)
			}
		}
		for k, v := range spec.Headers {
			if strings.Join(again.Headers.Values(k), "\x00") != strings.Join(v, "\x00") {
				t.Fatalf("header %q changed\nin:  %q\nout: %q\n%q vs %q", k, cmd, out, v, again.Headers.Values(k))
			}
		}
	})
}

func FuzzCompareVersions(f *testing.F) {
	f.Add("0.9.10", "0.9.9")
	f.Add("v1.0.0-rc1", "1.0.0")
	f.Fuzz(func(t *testing.T, a, b string) {
		if CompareVersions(a, b) != -CompareVersions(b, a) {
			t.Fatalf("asymmetric: %q %q", a, b)
		}
	})
}

func FuzzToolboxAndHTTPTarget(f *testing.F) {
	f.Add("modbus", "01 03", "http://h:1/a", "/b?c=d")
	f.Fuzz(func(t *testing.T, kind, in, base, p string) {
		_ = ParseToolbox(kind, in)
		_ = resolveHTTPTarget(base, p)
		_, _ = ParseData(in, true, "LF")
		_ = sha256FromNotes(in, kind)
	})
}

// normMethod 按实际发送时的规则归一方法:空白方法在有请求体时按 POST 发,否则 GET。
func normMethod(spec HTTPRequestSpec) string {
	m := strings.ToUpper(strings.TrimSpace(spec.Method))
	if m == "" {
		if len(spec.Data) > 0 || len(spec.Form) > 0 || len(spec.Body) > 0 {
			return "POST"
		}
		return "GET"
	}
	return m
}

// 进制转换与 $'...' 解析的模糊测试;深挖用 -fuzz 指定函数名。

func FuzzToolboxKinds(f *testing.F) {
	for _, s := range []string{"01 02", "-129", "18446744073709551615", "1727251200123", "41 0D 0A E4 B8", "", "-9223372036854775808"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, k := range []string{"hex2text", "text2hex", "hex2dec", "dec2hex", "unixtime", "base64dec", "base64enc", "modbus"} {
			out := ParseToolbox(k, in)
			if !utf8.ValidString(out) {
				t.Fatalf("%s(%q) produced invalid UTF-8: %q", k, in, out)
			}
		}
		// text2hex → hex2text 往返:可打印且合法的 UTF-8 应原样回来
		if in != "" && utf8.ValidString(in) && !strings.ContainsAny(in, "\\") {
			printable := true
			for _, r := range in {
				if r < 0x20 || r == 0x7f {
					printable = false
				}
			}
			if printable {
				if back := ParseToolbox("hex2text", ParseToolbox("text2hex", in)); back != in {
					t.Fatalf("round trip %q -> %q", in, back)
				}
			}
		}
		// dec2hex → hex2dec:十进制往返
		if out := ParseToolbox("dec2hex", in); strings.HasPrefix(out, "0x") {
			lines := strings.Split(out, "\n")
			be := strings.TrimPrefix(lines[1], "大端：")
			dec := ParseToolbox("hex2dec", be)
			want := strings.TrimSpace(in)
			if n, err := strconv.ParseInt(want, 10, 64); err == nil && n < 0 {
				if !strings.Contains(dec, "有符号 大端："+strconv.FormatInt(n, 10)+"；") {
					t.Fatalf("dec2hex(%q)=%q, hex2dec=%q", in, out, dec)
				}
			} else if u, err := strconv.ParseUint(strings.TrimPrefix(want, "-"), 10, 64); err == nil {
				if !strings.HasPrefix(dec, "大端："+strconv.FormatUint(u, 10)+"；") {
					t.Fatalf("dec2hex(%q)=%q, hex2dec=%q", in, out, dec)
				}
			}
		}
	})
}

func FuzzCURLANSIC(f *testing.F) {
	for _, s := range []string{`curl --data-raw $'{"a":"it\'s\\n中\x41\101\cA"}' http://x/`, `curl $'http://x/\t'`, `curl -H $'X: \e[0m' http://x`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		spec, err := ParseCURL(cmd)
		if err != nil {
			return
		}
		out, err := FormatCURL(spec)
		if err != nil {
			return
		}
		again, err := ParseCURL(out)
		if err != nil {
			t.Fatalf("export rejected\nin:  %q\nout: %q\nerr: %v", cmd, out, err)
		}
		if again.URL != spec.URL || len(again.Data) != len(spec.Data) {
			t.Fatalf("changed\nin:  %q\nout: %q", cmd, out)
		}
		for i := range spec.Data {
			if spec.Data[i] != again.Data[i] {
				t.Fatalf("data changed\nin:  %q\nout: %q\n%q\n%q", cmd, out, spec.Data[i].Value, again.Data[i].Value)
			}
		}
	})
}
