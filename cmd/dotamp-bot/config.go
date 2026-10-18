package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Config is config.json. Secrets are never in it, only paths to files.
type Config struct {
	DiscordTokenFile string   `json:"discord_token_file"`
	AllowGuilds      []string `json:"allow_guilds"`
	OwnerID          string   `json:"owner_id"`
	DJRole           string   `json:"dj_role"`            // a role name or ID
	Autoplay         *bool    `json:"autoplay"`           // nil = on
	IdleLeaveMinutes int      `json:"idle_leave_minutes"` // 0 = 5
	Plex             *struct {
		URL       string `json:"url"`
		TokenFile string `json:"token_file"`
	} `json:"plex"`
	Navidrome *struct {
		URL          string `json:"url"`
		User         string `json:"user"`
		PasswordFile string `json:"password_file"`
	} `json:"navidrome"`
	HistoryDir string `json:"history_dir"`
}

type secrets struct{ discord, plex, navidrome string }

func (c Config) autoplay() bool { return c.Autoplay == nil || *c.Autoplay }

func (c Config) idleLeave() time.Duration {
	if c.IdleLeaveMinutes <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(c.IdleLeaveMinutes) * time.Minute
}

func allowedGuild(c Config, id snowflake.ID) bool {
	for _, g := range c.AllowGuilds {
		if g == id.String() {
			return true
		}
	}
	return false
}

// loadConfig reads config.json and the secret files it names. It refuses
// a config that holds a secret inline or allows no server.
func loadConfig(path string) (Config, secrets, error) {
	var cfg Config
	var sec secrets
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, sec, err
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return cfg, sec, fmt.Errorf("%s: %w", path, err)
	}
	if key := inlineSecret(raw); key != "" {
		return cfg, sec, fmt.Errorf("config.json must not hold secrets: put %q in a file and name it with a _file key", key)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, sec, fmt.Errorf("%s: %w", path, err)
	}
	if len(cfg.AllowGuilds) == 0 {
		return cfg, sec, errors.New("allow_guilds is empty: name at least one Discord server")
	}
	read := func(p string) (string, error) {
		b, err := os.ReadFile(p)
		return strings.TrimSpace(string(b)), err
	}
	if sec.discord, err = read(cfg.DiscordTokenFile); err != nil {
		return cfg, sec, fmt.Errorf("discord token: %w", err)
	}
	if cfg.Plex != nil {
		if sec.plex, err = read(cfg.Plex.TokenFile); err != nil {
			return cfg, sec, fmt.Errorf("plex token: %w", err)
		}
	}
	if cfg.Navidrome != nil {
		if sec.navidrome, err = read(cfg.Navidrome.PasswordFile); err != nil {
			return cfg, sec, fmt.Errorf("navidrome password: %w", err)
		}
	}
	return cfg, sec, nil
}

// inlineSecret finds a key that looks like a secret itself rather than a
// path to one.
func inlineSecret(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			l := strings.ToLower(k)
			if l == "token" || l == "password" || strings.HasSuffix(l, "_token") || strings.HasSuffix(l, "_password") {
				return k
			}
			if k := inlineSecret(x); k != "" {
				return k
			}
		}
	case []any:
		for _, x := range t {
			if k := inlineSecret(x); k != "" {
				return k
			}
		}
	}
	return ""
}
