package wincore

// 进制转换与 $'...' 解析的模糊测试;深挖用 -fuzz 指定函数名。

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

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
