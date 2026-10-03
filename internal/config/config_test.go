package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	if got := Path(); got != "/tmp/xdg/dotamp/config.json" {
		t.Fatalf("Path() = %q", got)
	}
}

func TestCacheDirMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("mac only")
	}
	home, _ := os.UserHomeDir()
	if got := CacheDir(); got != filepath.Join(home, "Library", "Caches", "dotamp") {
		t.Fatalf("CacheDir() = %q", got)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Load(); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLoadFillsDefaultsAndPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{Server: "http://plex:32400", Token: "abc"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID == "" || c.Volume != 0.8 {
		t.Fatalf("defaults not filled: %+v", c)
	}
	again, _ := Load()
	if again.ClientID != c.ClientID {
		t.Fatal("client id not stable across loads")
	}
}

func TestLoadRejectsEmptyServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = Save(Config{Token: "abc"})
	if _, err := Load(); err == nil {
		t.Fatal("expected error for empty server")
	}
}

func TestLoadAcceptsAccountTokenWithoutServer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{AccountToken: "acct"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.AccountToken != "acct" || c.ClientID == "" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
	if !c.Manual() {
		// no server/token: must not count as a manual server
	} else {
		t.Fatal("Manual() should be false without server and token")
	}
	_ = Save(Config{Server: "http://x", Token: "t", AccountToken: "acct"})
	c, _ = Load()
	if !c.Manual() {
		t.Fatal("Manual() should be true with server and token")
	}
}

func TestLoadRejectsNeitherServerNorAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = Save(Config{ClientID: "x"})
	if _, err := Load(); err == nil {
		t.Fatal("expected an error with neither a server nor an account token")
	}
}
