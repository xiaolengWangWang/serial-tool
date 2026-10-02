//go:build ignore

// Generate from Git's plaintext view: document protection on some Windows
// machines exposes encrypted .md bytes to the Go compiler's embed reader.
package main

import (
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	data, err := exec.Command("git", "show", ":docs/CommBox使用手册.md").Output()
	if err != nil {
		panic(err)
	}
	manual := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(manual, "# CommBox 使用手册") {
		panic("manual must be plaintext UTF-8")
	}
	source := "// Code generated from CommBox使用手册.md; DO NOT EDIT.\n// Stage that document, then run go generate ./docs.\n//go:generate go run generate.go\n\npackage docs\n\nconst Manual = " + strconv.Quote(manual) + "\n"
	code, err := format.Source([]byte(source))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("manual.go", code, 0644); err != nil {
		panic(err)
	}
	fmt.Println("Generated offline manual")
}
