package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

func TestPermissionTable(t *testing.T) {
	cases := []struct {
		cmd                  string
		dj, inChannel, alone bool
		want                 bool
	}{
		{"join", false, false, false, true},
		{"play", false, true, false, true}, {"play", false, false, false, false},
		{"queue", false, true, false, true}, {"np", false, true, false, true},
		{"skip", false, true, false, true},
		{"remove", false, true, false, true},
		{"pause", false, true, false, false}, {"pause", true, true, false, true},
		{"clear", false, true, false, false}, {"clear", true, false, false, true},
		{"autoplay", false, true, false, false}, {"autoplay", true, true, false, true},
		{"leave", false, true, false, false}, {"leave", false, true, true, true}, {"leave", true, false, false, true},
	}
	for _, c := range cases {
		if got := allowed(c.cmd, c.dj, c.inChannel, c.alone); got != c.want {
			t.Fatalf("%+v → %v", c, got)
		}
	}
}

func TestDJIsTheRoleOrTheOwner(t *testing.T) {
	name := func(id snowflake.ID) string {
		if id == 7 {
			return "DJ"
		}
		return "member"
	}
	if !isDJ([]snowflake.ID{3, 7}, name, "u1", "owner", "DJ") || !isDJ(nil, name, "owner", "owner", "DJ") {
		t.Fatal("role or owner must be DJ")
	}
	if !isDJ([]snowflake.ID{7}, func(snowflake.ID) string { return "x" }, "u1", "owner", "7") {
		t.Fatal("role by id")
	}
	if isDJ([]snowflake.ID{3}, name, "u1", "owner", "DJ") {
		t.Fatal("not a DJ")
	}
}

func TestConfigLoadsSecretsFromFilesAndGuardsTheAllowlist(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "discord-token"), []byte("tok\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "plex-token"), []byte("ptok\n"), 0o600)
	write := func(s string) string {
		p := filepath.Join(dir, "config.json")
		os.WriteFile(p, []byte(s), 0o600)
		return p
	}
	good := fmt.Sprintf(`{"discord_token_file":%q,"allow_guilds":["665728936340357130"],"owner_id":"1","dj_role":"DJ","plex":{"url":"http://p","token_file":%q},"history_dir":%q}`,
		filepath.Join(dir, "discord-token"), filepath.Join(dir, "plex-token"), dir)
	cfg, sec, err := loadConfig(write(good))
	if err != nil || sec.discord != "tok" || sec.plex != "ptok" || !cfg.autoplay() || cfg.idleLeave() != 5*time.Minute {
		t.Fatalf("%v %+v", err, cfg)
	}
	if !allowedGuild(cfg, 665728936340357130) || allowedGuild(cfg, 1) {
		t.Fatal("allowlist")
	}
	if _, _, err := loadConfig(write(`{"discord_token_file":"x","allow_guilds":[]}`)); err == nil {
		t.Fatal("an empty allowlist must be refused")
	}
	if _, _, err := loadConfig(write(`{"discord_token":"abc","allow_guilds":["1"]}`)); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("an inline token must be refused: %v", err)
	}
	if _, _, err := loadConfig(write(`{"discord_token_file":"/nope","allow_guilds":["1"]}`)); err == nil {
		t.Fatal("a missing token file must be refused")
	}
}

func TestJoinRefusesWhenBusyElsewhere(t *testing.T) {
	if r, move := joinReply(10, "a-team", 11, 2); move || r.Text != "Busy in a-team; join there or wait" {
		t.Fatalf("%+v %v", r, move)
	}
	if _, move := joinReply(10, "a-team", 11, 0); !move {
		t.Fatal("must move from an empty channel")
	}
	if r, move := joinReply(0, "", 11, 0); !move || r.Text != "" {
		t.Fatalf("%+v %v", r, move)
	}
	if r, move := joinReply(11, "airwolf", 11, 1); move || r.Text != "Already playing in airwolf" {
		t.Fatalf("%+v %v", r, move)
	}
}
