package wincore

// 解析类函数的模糊测试。平时 go test 只跑种子;深挖用
//   go test -run '^$' -fuzz '^FuzzCURLRoundTrip$' -fuzztime 2m ./internal/wincore/

import (
	"strings"
	"testing"
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
		if normMethod(again.Method) != normMethod(spec.Method) || again.URL != spec.URL || len(again.Data) != len(spec.Data) || len(again.Form) != len(spec.Form) || again.Insecure != spec.Insecure || again.FollowRedirects != spec.FollowRedirects {
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

func normMethod(m string) string {
	m = strings.ToUpper(strings.TrimSpace(m))
	if m == "" {
		return "GET"
	}
	return m
}
