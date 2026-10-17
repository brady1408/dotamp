package broadcast

import (
	"context"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
)

// commandDefs is what the bot registers on each server.
func commandDefs() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "play", Description: "Add a track, album or playlist to the queue",
			Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
				Name: "what", Description: "Search the DJ's library", Required: true, Autocomplete: true}}},
		discord.SlashCommandCreate{Name: "np", Description: "What is playing"},
		discord.SlashCommandCreate{Name: "queue", Description: "What is coming up and who asked"},
	}
}

// onCommand turns a slash command into a command for the core and sends
// the reply. An album or playlist needs a library round trip that can
// outlast Discord's 3 s, so those are acknowledged first and the reply
// edited in; the acknowledgement fixes the reply as public.
func (b *Broadcaster) onCommand(e *events.ApplicationCommandInteractionCreate) {
	if !b.cfg.Commands || e.GuildID() == nil {
		return
	}
	data := e.SlashCommandInteractionData()
	user := e.User()
	name := user.EffectiveName()
	if m := e.Member(); m != nil {
		name = m.EffectiveName()
	}
	c := command{Name: data.CommandName(), UserID: user.ID.String(), UserName: name, Guild: *e.GuildID()}
	if c.Name == "play" {
		c.What = data.String("what")
	}
	if ch, ok := b.deps.voiceOf(c.Guild, user.ID); ok {
		c.Voice = ch
	}
	deferred := strings.HasPrefix(c.What, "a:") || strings.HasPrefix(c.What, "p:")
	if deferred {
		if err := e.DeferCreateMessage(false); err != nil {
			b.cfg.Logger.Warn("command defer", "err", err)
			return
		}
	}
	go func() {
		defer b.guard("command")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r := b.command(ctx, c)
		var err error
		if deferred {
			_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{Content: &r.Text}, rest.WithCtx(ctx))
		} else {
			msg := discord.MessageCreate{Content: r.Text}
			if !r.Public {
				msg.Flags = discord.MessageFlagEphemeral
			}
			err = e.CreateMessage(msg, rest.WithCtx(ctx))
		}
		if err != nil {
			b.cfg.Logger.Warn("command reply", "err", err)
		}
	}()
}

func (b *Broadcaster) onAutocomplete(e *events.AutocompleteInteractionCreate) {
	if !b.cfg.Commands {
		return
	}
	q := e.Data.String("what")
	go func() {
		defer b.guard("autocomplete")
		if err := e.AutocompleteResult(b.complete(context.Background(), q)); err != nil {
			b.cfg.Logger.Warn("autocomplete reply", "err", err)
		}
	}()
}

// onGuildJoin registers the commands on a server the bot was just invited to.
func (b *Broadcaster) onGuildJoin(e *events.GuildJoin) {
	if !b.cfg.Commands {
		return
	}
	go func() {
		defer b.guard("guild join")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.deps.register(ctx, e.GuildID); err != nil {
			b.cfg.Logger.Warn("register commands", "guild", e.GuildID, "err", err)
		}
	}()
}
