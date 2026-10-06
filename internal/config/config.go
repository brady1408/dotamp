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
	Server   string  `json:"server,omitempty"` // manual server URL; with Token it overrides discovery
	Token    string  `json:"token,omitempty"`
	ClientID string  `json:"client_id"`
	Section  string  `json:"section,omitempty"`
	Volume   float64 `json:"volume"`

	AccountToken  string     `json:"account_token,omitempty"` // from `dotamp login`
	ServerName    string     `json:"server_name,omitempty"`   // which of the account's servers to use; first owned when empty
	LastServer    string     `json:"last_server,omitempty"`   // the connection discovery chose last time, for starts without plex.tv
	LastToken     string     `json:"last_token,omitempty"`
	LastServerID  string     `json:"last_server_id,omitempty"`
	RemoteBitrate int        `json:"remote_bitrate,omitempty"` // kbps to transcode to on any non-local connection; 0 = original
	Visual        string     `json:"visual,omitempty"`         // analyzer mode: "bars", "scope", "spectrogram" or "stereo"
	Navidrome     *Navidrome `json:"navidrome,omitempty"`
}

// Navidrome is an optional second library on a Subsonic-compatible server.
type Navidrome struct {
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// Manual reports whether a hand-written server and token are set.
func (c Config) Manual() bool { return c.Server != "" && c.Token != "" }

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
	if !c.Manual() && c.AccountToken == "" && c.Navidrome == nil {
		return Config{}, fmt.Errorf("%s: run `dotamp login`, or set server and token", Path())
	}
	dirty := false
	if c.ClientID == "" {
		c.ClientID = NewClientID()
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

// NewClientID is the stable identity Plex shows for this install.
func NewClientID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
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
