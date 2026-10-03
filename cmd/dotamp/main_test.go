package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestFirstRunWithoutConfigExplains(t *testing.T) {
	bin := t.TempDir() + "/dotamp"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit without config")
	}
	if !strings.Contains(string(out), "config.json") || !strings.Contains(string(out), "\"server\"") {
		t.Fatalf("output should name the config path and show an example:\n%s", out)
	}
}
