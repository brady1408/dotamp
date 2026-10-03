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
