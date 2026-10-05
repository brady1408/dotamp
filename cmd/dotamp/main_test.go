package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func build(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/dotamp"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestFirstRunWithoutConfigExplains(t *testing.T) {
	bin := build(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit without config")
	}
	if !strings.Contains(string(out), "dotamp login") || !strings.Contains(string(out), "config.json") {
		t.Fatalf("output should point at `dotamp login` and the config path:\n%s", out)
	}
}

// fakePlex is plex.tv and a media server in one: pins approve at once, the
// account owns one server whose only connection is this very URL.
func fakePlex(t *testing.T) *httptest.Server {
	t.Helper()
	var s *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/pins", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "code": "WXYZ"})
	})
	mux.HandleFunc("GET /api/v2/pins/7", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "code": "WXYZ", "authToken": "acct-token"})
	})
	mux.HandleFunc("GET /api/v2/resources", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "acct-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name": "testbox", "provides": "server", "clientIdentifier": "tb", "owned": true, "accessToken": "srv-token",
			"connections": []map[string]any{{"uri": s.URL, "local": true, "relay": false}},
		}})
	})
	mux.HandleFunc("GET /api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"username":"tester","subscription":{"active":false}}`))
	})
	mux.HandleFunc("GET /identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "srv-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"tb"}}`))
	})
	s = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestLoginSavesAccountTokenAndFindsServer(t *testing.T) {
	bin := build(t)
	plex := fakePlex(t)
	home := t.TempDir()
	cmd := exec.Command(bin, "login")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "DOTAMP_PLEXTV="+plex.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("login failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "app.plex.tv/auth") || !strings.Contains(string(out), "WXYZ") || !strings.Contains(string(out), "testbox") {
		t.Fatalf("login output should show the link, the code and the server found:\n%s", out)
	}
	b, err := os.ReadFile(filepath.Join(home, "dotamp", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(b, &cfg)
	if cfg["account_token"] != "acct-token" || cfg["last_server"] != plex.URL || cfg["last_token"] != "srv-token" {
		t.Fatalf("config = %s", b)
	}
	if _, has := cfg["token"]; has {
		t.Fatal("login must not write a manual server token")
	}
}

func TestServersCommandListsReachability(t *testing.T) {
	bin := build(t)
	plex := fakePlex(t)
	home := t.TempDir()
	login := exec.Command(bin, "login")
	login.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "DOTAMP_PLEXTV="+plex.URL)
	if out, err := login.CombinedOutput(); err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "servers")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "DOTAMP_PLEXTV="+plex.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("servers: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "testbox") || !strings.Contains(string(out), "local") || !strings.Contains(string(out), plex.URL) {
		t.Fatalf("servers output:\n%s", out)
	}
	if !strings.Contains(string(out), "Signed in as tester") {
		t.Fatalf("servers output should name the account:\n%s", out)
	}
}
