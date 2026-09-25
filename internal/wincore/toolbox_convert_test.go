package wincore

import (
	"strings"
	"testing"
	"time"
)

func TestToolboxConversions(t *testing.T) {
	cases := []struct{ kind, in, want string }{
		{"text2hex", "AT\r\n", "41 54 0D 0A"},
		{"text2hex", "中", "E4 B8 AD"},
		{"hex2text", "41 54 0D 0A", `AT\r\n`},
		{"hex2text", "E4 B8 AD 00 FF 09", `中\x00\xFF\t`},
		{"hex2dec", "01 00", "大端：256；小端：1；有符号 大端：256；小端：1\n逐字节：1 0"},
		{"hex2dec", "FF FF", "大端：65535；小端：65535；有符号 大端：-1；小端：-1\n逐字节：255 255"},
		{"hex2dec", "01 02 03", "大端：66051；小端：197121\n逐字节：1 2 3"},
		{"dec2hex", "255", "0xFF（1 字节）\n大端：FF\n小端：FF"},
		{"dec2hex", "256", "0x0100（2 字节）\n大端：01 00\n小端：00 01"},
		{"dec2hex", "-1", "0xFF（1 字节）\n大端：FF\n小端：FF"},
		{"dec2hex", "-129", "0xFF7F（2 字节）\n大端：FF 7F\n小端：7F FF"},
		{"dec2hex", "18446744073709551615", "0xFFFFFFFFFFFFFFFF（8 字节）\n大端：FF FF FF FF FF FF FF FF\n小端：FF FF FF FF FF FF FF FF"},
		{"dec2hex", "abc", "无效的十进制整数（范围 -9223372036854775808 ~ 18446744073709551615）"},
		{"hex2text", "zz", "输入不是有效的 HEX"},
	}
	for _, c := range cases {
		if got := ParseToolbox(c.kind, c.in); got != c.want {
			t.Errorf("ParseToolbox(%q, %q) = %q, want %q", c.kind, c.in, got, c.want)
		}
	}
	if got := ParseToolbox("hex2dec", strings.Repeat("01 ", 9)); !strings.Contains(got, "超过 8 字节") {
		t.Errorf("9 bytes: %q", got)
	}
}

func TestUnixToTimeDetectsMilliseconds(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	t.Cleanup(func() { time.Local = old })
	if got := UnixToTime(1727251200); got != "2024-09-25 16:00:00 CST（按秒）" {
		t.Errorf("seconds = %q", got)
	}
	if got := UnixToTime(1727251200123); got != "2024-09-25 16:00:00.123 CST（按毫秒）" {
		t.Errorf("milliseconds = %q", got)
	}
}
