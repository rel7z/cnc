// Harmless Linux-binary fixture. No network requests or command-line arguments.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) != 1 {
		panic("this fixture accepts no arguments")
	}
	executable, err := os.Executable()
	must(err)
	cwd, err := os.Getwd()
	must(err)
	if filepath.Dir(executable) != cwd {
		panic("entrypoint and working directory differ")
	}
	support, err := os.ReadFile("pass.txt")
	must(err)
	if string(support) != "fixture-support\n" {
		panic("missing or shared supporting file")
	}
	input, err := os.ReadFile("wordpress.txt")
	must(err)
	lines := strings.Split(strings.TrimSuffix(string(input), "\n"), "\n")
	if len(lines) == 0 || len(lines) > 1000 {
		panic("invalid input chunk")
	}
	must(os.WriteFile("results.txt", input, 0600))
	time.Sleep(2 * time.Second)
	must(os.WriteFile("pass.txt", []byte("changed in this isolated job only\n"), 0600))
	fmt.Printf("processed=%d\n", len(lines))
}
