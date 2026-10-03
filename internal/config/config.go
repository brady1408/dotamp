package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	Server   string  `json:"server"`
	Token    string  `json:"token"`
	ClientID string  `json:"client_id"`
	Section  string  `json:"section,omitempty"`
	Volume   float64 `json:"volume"`
}

var ErrNotFound = errors.New("config not found")

func Path() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "dotamp", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dotamp", "config.json")
}

func CacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "dotamp")
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", "dotamp")
	}
	return filepath.Join(home, ".cache", "dotamp")
}

func Load() (Config, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", Path(), err)
	}
	if c.Server == "" || c.Token == "" {
		return Config{}, fmt.Errorf("%s: server and token are required", Path())
	}
	dirty := false
	if c.ClientID == "" {
		var raw [16]byte
		_, _ = rand.Read(raw[:])
		c.ClientID = hex.EncodeToString(raw[:])
		dirty = true
	}
	if c.Volume <= 0 {
		c.Volume = 0.8
		dirty = true
	}
	if dirty {
		if err := Save(c); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

func Save(c Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}
