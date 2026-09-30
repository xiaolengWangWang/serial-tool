//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == pdfWorkerFlag {
		os.Exit(runAIPDFWorker(os.Args[2]))
	}
	os.Exit(m.Run())
}

func TestReadPDFWorker(t *testing.T) {
	fixture := filepath.Join("..", "internal", "aiattachment", "testdata", "sample.pdf")
	a, err := readPDFAttachment(fixture)
	if err != nil || !strings.Contains(a.Text, "Hello PDF") {
		t.Fatalf("PDF worker: attachment=%+v err=%v", a, err)
	}
}
