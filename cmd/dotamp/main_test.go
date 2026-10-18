package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/config"
	"github.com/brady1408/dotamp/internal/control"
	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
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
	cmd.Stdin = strings.NewReader("") // no answers at all
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
		}, {
			"name": "musicbox", "provides": "server", "clientIdentifier": "mb", "owned": false, "accessToken": "music-token",
			"connections": []map[string]any{{"uri": s.URL, "local": true, "relay": false}},
		}})
	})
	// The same fake answers for both servers; the token says which one.
	mux.HandleFunc("GET /library/sections", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Plex-Token") {
		case "srv-token": // testbox: video only
			_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie","title":"Movies"}]}}`))
		case "music-token": // musicbox: has music
			_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"7","type":"artist","title":"Music"}]}}`))
		default:
			http.Error(w, "unauthorized", 401)
		}
	})
	mux.HandleFunc("GET /api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"username":"tester","subscription":{"active":false}}`))
	})
	mux.HandleFunc("GET /identity", func(w http.ResponseWriter, r *http.Request) {
		if tok := r.Header.Get("X-Plex-Token"); tok != "srv-token" && tok != "music-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"tb"}}`))
	})
	s = httptest.NewUnstartedServer(mux) // assigned before Start, so handlers may read s.URL
	s.Start()
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

func fakeNavidromeServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/ping" || r.URL.Query().Get("u") != "brady" || r.URL.Query().Get("t") == "" {
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"failed","version":"1.16.1","error":{"code":40,"message":"Wrong username or password"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","version":"1.16.1","type":"navidrome","serverVersion":"0.60.3"}}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestNavidromeCommandPromptsAndSaves(t *testing.T) {
	bin := build(t)
	nd := fakeNavidromeServer(t)
	home := t.TempDir()
	cmd := exec.Command(bin, "navidrome", nd.URL, "brady")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home)
	cmd.Stdin = strings.NewReader("hunter2\n") // not a terminal: the password is read from stdin
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("navidrome: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0.60.3") {
		t.Fatalf("should report the server version it reached:\n%s", out)
	}
	b, _ := os.ReadFile(filepath.Join(home, "dotamp", "config.json"))
	var cfg map[string]any
	_ = json.Unmarshal(b, &cfg)
	nav, _ := cfg["navidrome"].(map[string]any)
	if nav == nil || nav["url"] != nd.URL || nav["user"] != "brady" || nav["password"] != "hunter2" {
		t.Fatalf("config = %s", b)
	}
	// A wrong password is refused and nothing is saved over the good one.
	bad := exec.Command(bin, "navidrome", nd.URL, "someone")
	bad.Env = cmd.Env
	bad.Stdin = strings.NewReader("nope\n")
	if out, err := bad.CombinedOutput(); err == nil || !strings.Contains(string(out), "Wrong username") {
		t.Fatalf("expected a refusal: %v\n%s", err, out)
	}
	b2, _ := os.ReadFile(filepath.Join(home, "dotamp", "config.json"))
	if string(b2) != string(b) {
		t.Fatal("a failed check must not change the config")
	}
}

func TestFirstRunWalksThroughSetup(t *testing.T) {
	bin := build(t)
	plex := fakePlex(t)
	nd := fakeNavidromeServer(t)
	home := t.TempDir()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "DOTAMP_PLEXTV="+plex.URL, "TERM=")
	// Answers: both, then Navidrome's URL, user, password.
	cmd.Stdin = strings.NewReader("3\n" + nd.URL + "\nbrady\nhunter2\n")
	out, _ := cmd.CombinedOutput() // the player itself cannot open without a terminal; that failure is fine here
	text := string(out)
	for _, want := range []string{"Which music server", "app.plex.tv/auth", "testbox", "Navidrome", "0.60.3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("setup output lacks %q:\n%s", want, text)
		}
	}
	b, err := os.ReadFile(filepath.Join(home, "dotamp", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(b, &cfg)
	nav, _ := cfg["navidrome"].(map[string]any)
	if cfg["account_token"] != "acct-token" || nav == nil || nav["user"] != "brady" {
		t.Fatalf("config after setup = %s", b)
	}
}

func TestFirstRunCanSkipPlex(t *testing.T) {
	bin := build(t)
	nd := fakeNavidromeServer(t)
	home := t.TempDir()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "TERM=")
	cmd.Stdin = strings.NewReader("2\n" + nd.URL + "\nbrady\nhunter2\n")
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "app.plex.tv") {
		t.Fatalf("choosing Navidrome only must not start the Plex login:\n%s", out)
	}
	b, _ := os.ReadFile(filepath.Join(home, "dotamp", "config.json"))
	var cfg map[string]any
	_ = json.Unmarshal(b, &cfg)
	if _, has := cfg["account_token"]; has || cfg["navidrome"] == nil {
		t.Fatalf("config = %s", b)
	}
}

func TestFirstRunDecliningEverythingExplains(t *testing.T) {
	bin := build(t)
	home := t.TempDir()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "TERM=")
	cmd.Stdin = strings.NewReader("\n")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "dotamp login") {
		t.Fatalf("expected a non-zero exit with the commands named:\n%s", out)
	}
}

func TestFallsBackToAServerThatHasMusic(t *testing.T) {
	plex := fakePlex(t)
	t.Setenv("DOTAMP_PLEXTV", plex.URL)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // the fallback saves config; never the real one
	cfg := &config.Config{AccountToken: "acct-token", ClientID: "cid"}
	c, srv, err := fallbackMusicServer(context.Background(), cfg, "tb", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID != "mb" || srv.Name != "musicbox" || srv.Owned {
		t.Fatalf("server = %+v", srv)
	}
	if c == nil || cfg.Section != "7" || cfg.LastToken != "music-token" || cfg.LastServerID != "mb" {
		t.Fatalf("client=%v section=%q lastToken=%q lastID=%q", c != nil, cfg.Section, cfg.LastToken, cfg.LastServerID)
	}
	if _, _, err := fallbackMusicServer(context.Background(), cfg, "mb", 1000); err == nil || !strings.Contains(err.Error(), "testbox") {
		t.Fatalf("with only video servers left the error names them: %v", err)
	}
}

func TestDiscordCommandSavesUserAndChecksTheTokenFile(t *testing.T) {
	bin := build(t)
	home := t.TempDir()
	cmd := exec.Command(bin, "discord", "123456789012345678")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "token file missing") {
		t.Fatalf("without a token file the command must say so: %v\n%s", err, out)
	}
	_ = os.MkdirAll(filepath.Join(home, "dotamp"), 0o755)
	_ = os.WriteFile(filepath.Join(home, "dotamp", "discord-token"), []byte("not-a-real-token\n"), 0o600)
	cmd = exec.Command(bin, "discord", "123456789012345678")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+home, "DOTAMP_DISCORD_NOCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(home, "dotamp", "config.json"))
	var cfg map[string]any
	_ = json.Unmarshal(b, &cfg)
	d, _ := cfg["discord"].(map[string]any)
	if d == nil || d["user_id"] != "123456789012345678" {
		t.Fatalf("config = %s", b)
	}
	if strings.Contains(string(b), "not-a-real-token") {
		t.Fatal("the token must never be written to config.json")
	}
	if out, err := exec.Command(bin, "discord", "notanumber").CombinedOutput(); err == nil || !strings.Contains(string(out), "usage") {
		t.Fatalf("a bad id must show usage: %v\n%s", err, out)
	}
}

func TestNightAdapterSkipsTracksFromUnreachableServers(t *testing.T) {
	dir := t.TempDir()
	w := history.NewWriter(dir)
	w.Append("g1", history.Entry{At: time.Now(), Track: library.Track{ID: "a:1", Server: "a", Duration: time.Minute}, Played: 60})
	w.Append("g1", history.Entry{At: time.Now(), Track: library.Track{ID: "b:1", Server: "b", Duration: time.Minute}, Played: 60})
	n := nights{dir: dir, servers: func() []string { return []string{"a"} }, name: func(id string) string { return "Kicarus" }}
	name, ts, skipped, err := n.Night(context.Background(), "g1")
	if err != nil || name != "Kicarus" || len(ts) != 1 || ts[0].ID != "a:1" || skipped != 1 {
		t.Fatalf("%q %v %d %v", name, ts, skipped, err)
	}
}

func TestParseCtlArgs(t *testing.T) {
	cases := []struct {
		args []string
		want control.Request
	}{
		{[]string{"status"}, control.Request{Op: "status"}},
		{[]string{"search", "collective soul"}, control.Request{Op: "search", Q: "collective soul"}},
		{[]string{"add", "--as", "Claude", "t:s:1", "a:s:9"}, control.Request{Op: "add", As: "Claude", IDs: []string{"t:s:1", "a:s:9"}}},
		{[]string{"playlist-create", "Road Trip", "t:s:1", "t:s:2"}, control.Request{Op: "playlist_create", Name: "Road Trip", IDs: []string{"t:s:1", "t:s:2"}}},
		{[]string{"playlist-add", "p:s:p1", "t:s:7"}, control.Request{Op: "playlist_add", ID: "p:s:p1", IDs: []string{"t:s:7"}}},
		{[]string{"album", "a:s:9"}, control.Request{Op: "album", ID: "a:s:9"}},
	}
	for _, c := range cases {
		got, err := parseCtl(c.args)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%v → %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
	if got, _ := parseCtl([]string{"remove", "2"}); got.Index == nil || *got.Index != 2 {
		t.Fatalf("remove index = %+v", got.Index)
	}
	if _, err := parseCtl([]string{"remove", "x"}); err == nil {
		t.Fatal("a non-number index must fail")
	}
	if _, err := parseCtl(nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no verb = %v", err)
	}
}

func TestCtlWithoutARunningPlayerSaysSo(t *testing.T) {
	bin := build(t)
	cmd := exec.Command(bin, "ctl", "status")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.Output()
	if err == nil || !strings.Contains(string(out), `"dotamp is not running"`) {
		t.Fatalf("out=%s err=%v", out, err)
	}
}
