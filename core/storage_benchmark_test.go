package core

import (
	"testing"
	"time"
)

func BenchmarkStoreRecord(b *testing.B) {
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if err := s.StartSession("TCP", "127.0.0.1:502", ""); err != nil {
		b.Fatal(err)
	}
	p := Packet{Source: "TCP", Data: []byte{1, 3, 2, 0, 100, 185, 175}}
	b.ReportAllocs()
	b.SetBytes(int64(len(p.Data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Timestamp = time.Now()
		if err := s.Record(p); err != nil {
			b.Fatal(err)
		}
	}
}
