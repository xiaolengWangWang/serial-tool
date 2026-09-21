//go:build windows

// A test-only process that owns real VirtualCOM pairs until stdin closes.
// Keeping the provider in a separate process verifies the published naming and
// byte-stream contract, including cleanup, without a test driver or fake pair.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"virtualcom/internal/vcom"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	m := vcom.NewManager()
	defer m.CloseAll()
	var pairs [][2]string
	for i := 0; i < 2; i++ {
		pair, err := m.Create("", "")
		if err != nil {
			return err
		}
		pairs = append(pairs, [2]string{pair.A.Name, pair.B.Name})
	}
	if err := json.NewEncoder(os.Stdout).Encode(pairs); err != nil {
		return err
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	return err
}
