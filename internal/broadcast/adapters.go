package broadcast

import (
	"context"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
)

// commandDefs is what the bot registers on each server: one /dotamp
// command, so its subcommands never share a name with another bot's.
func commandDefs() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "dotamp", Description: "Requests for the DJ's dotamp broadcast",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{Name: "play", Description: "Add a track, album or playlist to the queue",
					Options: []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{
						Name: "what", Description: "Search the DJ's library", Required: true, Autocomplete: true}}},
				discord.ApplicationCommandOptionSubCommand{Name: "np", Description: "What is playing"},
				discord.ApplicationCommandOptionSubCommand{Name: "queue", Description: "What is coming up and who asked"},
			}},
	}
}

// commandName is the subcommand of /dotamp, or "" for anything else.
func commandName(top string, sub *string) string {
	if top != "dotamp" || sub == nil {
		return ""
	}
	return *sub
}

// onCommand turns a slash command into a command for the core and sends
// the reply. Refusals that need no library round trip go out at once and
// stay private. A /play that passes them is acknowledged first, because
// the search or fetch behind it can outlast Discord's 3 s; that
// acknowledgement fixes the reply as public, which is what a successful
// add wants.
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
	c := command{Name: commandName(data.CommandName(), data.SubCommandName), UserID: user.ID.String(), UserName: name, Guild: *e.GuildID()}
	if c.Name == "" {
		return
	}
	if c.Name == "play" {
		c.What = data.String("what")
	}
	if ch, ok := b.deps.voiceOf(c.Guild, user.ID); ok {
		c.Voice = ch
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if r, refused := b.precheck(c); refused {
		defer cancel()
		if err := e.CreateMessage(discord.MessageCreate{Content: r.Text, Flags: discord.MessageFlagEphemeral}, rest.WithCtx(ctx)); err != nil {
			b.cfg.Logger.Warn("command reply", "err", err)
		}
		return
	}
	deferred := c.Name == "play"
	if deferred {
		if err := e.DeferCreateMessage(false, rest.WithCtx(ctx)); err != nil {
			cancel()
			b.cfg.Logger.Warn("command defer", "err", err)
			return
		}
	}
	cancel()
	go func() {
		defer b.guard("command")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	if commandName(e.Data.CommandName, e.Data.SubCommandName) != "play" {
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
