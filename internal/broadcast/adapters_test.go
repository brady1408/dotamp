package broadcast

import (
	"testing"

	"github.com/disgoorg/disgo/discord"
)

func TestCommandsAreSubcommandsOfDotamp(t *testing.T) {
	defs := commandDefs()
	if len(defs) != 1 {
		t.Fatalf("%d top-level commands; want one, /dotamp", len(defs))
	}
	top, ok := defs[0].(discord.SlashCommandCreate)
	if !ok || top.Name != "dotamp" {
		t.Fatalf("top = %+v", defs[0])
	}
	subs := map[string]discord.ApplicationCommandOptionSubCommand{}
	for _, o := range top.Options {
		if s, ok := o.(discord.ApplicationCommandOptionSubCommand); ok {
			subs[s.Name] = s
		}
	}
	for _, name := range []string{"play", "np", "queue"} {
		if _, ok := subs[name]; !ok {
			t.Fatalf("no /dotamp %s", name)
		}
	}
	what, ok := subs["play"].Options[0].(discord.ApplicationCommandOptionString)
	if !ok || what.Name != "what" || !what.Autocomplete || !what.Required {
		t.Fatalf("play option = %+v", subs["play"].Options)
	}
}

func TestCommandNameReadsTheSubcommand(t *testing.T) {
	play, other := "play", "x"
	if commandName("dotamp", &play) != "play" || commandName("dotamp", nil) != "" || commandName("play", &other) != "" {
		t.Fatal("only /dotamp <sub> is ours")
	}
}
