package main

import (
	"bytes"
	"testing"
)

func TestParseData(t *testing.T) {
	hexData, err := parseData("01 03 00 FF", true, nil)
	if err != nil || !bytes.Equal(hexData, []byte{1, 3, 0, 255}) {
		t.Fatalf("HEX 解析失败: % X, %v", hexData, err)
	}
	text, err := parseData("AT", false, []byte{'\r', '\n'})
	if err != nil || string(text) != "AT\r\n" {
		t.Fatalf("文本解析失败: %q, %v", text, err)
	}
}
