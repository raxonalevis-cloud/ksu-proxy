package main

import (
	"os"
	"os/exec"
)

func main() {
	bin := "/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/proxyd"
	if value := os.Getenv("PROXYD_BIN"); value != "" {
		bin = value
	}
	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
