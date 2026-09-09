package main

import "testing"

func TestPacketDisplayFields(t *testing.T) {
	got := packetDisplayFields("12:34:56.789", "RX", []byte{'A', 0, 0xff, ' '})
	want := packetDisplay{
		ts:    "12:34:56.789",
		dir:   "RX",
		hex:   "41 00 FF 20",
		ascii: "A.. ",
		len:   4,
	}
	if got != want {
		t.Fatalf("packet display fields = %#v, want %#v", got, want)
	}
}
