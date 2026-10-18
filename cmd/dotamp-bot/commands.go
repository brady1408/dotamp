package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/radio"
	"github.com/brady1408/dotamp/internal/requests"
	"github.com/brady1408/dotamp/internal/room"
)

func commandDefs() []discord.ApplicationCommandCreate {
	one, max := 1, 25
	what := func(desc string) []discord.ApplicationCommandOption {
		return []discord.ApplicationCommandOption{discord.ApplicationCommandOptionString{Name: "what", Description: desc, Required: true, Autocomplete: true}}
	}
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "join", Description: "Bring the bot into your voice channel"},
		discord.SlashCommandCreate{Name: "leave", Description: "Send the bot away"},
		discord.SlashCommandCreate{Name: "play", Description: "Add a track, album or playlist", Options: what("Search the library")},
		discord.SlashCommandCreate{Name: "queue", Description: "What is coming up and who asked", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionInt{Name: "count", Description: "How many songs (default 10)", MinValue: &one, MaxValue: &max},
			discord.ApplicationCommandOptionBool{Name: "public", Description: "Post it for everyone"},
		}},
		discord.SlashCommandCreate{Name: "np", Description: "What is playing"},
		discord.SlashCommandCreate{Name: "skip", Description: "Skip this song (or vote to)"},
		discord.SlashCommandCreate{Name: "remove", Description: "Take one of your songs out of the queue", Options: what("Your queued songs")},
		discord.SlashCommandCreate{Name: "pause", Description: "Pause or resume (DJ)"},
		discord.SlashCommandCreate{Name: "clear", Description: "Clear the queue (DJ)"},
		discord.SlashCommandCreate{Name: "autoplay", Description: "Radio when the queue runs out (DJ)", Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{Name: "mode", Description: "on or off", Required: true, Choices: []discord.ApplicationCommandOptionChoiceString{{Name: "on", Value: "on"}, {Name: "off", Value: "off"}}},
		}},
	}
}

func (b *houseBot) onCommand(e *events.ApplicationCommandInteractionCreate) {
	g := e.GuildID()
	if g == nil || !allowedGuild(b.cfg, *g) {
		return
	}
	r := b.rooms[*g]
	if r == nil {
		return
	}
	data := e.SlashCommandInteractionData()
	user := e.User()
	name := user.EffectiveName()
	var roles []snowflake.ID
	if m := e.Member(); m != nil {
		name, roles = m.EffectiveName(), m.RoleIDs
	}
	dj := isDJ(roles, b.roleName(*g), user.ID.String(), b.cfg.OwnerID, b.cfg.DJRole)
	userCh := b.voiceOf(*g, user.ID)
	roomCh, roomName := r.Where()
	inChannel := roomCh != 0 && userCh == roomCh
	alone := inChannel && b.listeners(*g, roomCh) <= 1
	cmd := data.CommandName()
	private := func(text string) { b.respond(e, reply{Text: text}, false) }
	if !allowed(cmd, dj, inChannel, alone) {
		switch {
		case roomCh == 0 && cmd != "pause" && cmd != "clear" && cmd != "autoplay" && cmd != "leave":
			private("I'm not playing; use /join")
		case !inChannel && (cmd == "play" || cmd == "queue" || cmd == "np" || cmd == "skip" || cmd == "remove"):
			private("Join " + roomName + " to request songs")
		default:
			private("That's for the DJ role")
		}
		return
	}
	if cmd == "play" && b.req.Expired(data.String("what")) {
		private("That choice expired; search again")
		return
	}
	if cmd == "join" && userCh == 0 {
		private("Join a voice channel first")
		return
	}
	deferred := cmd == "play" || cmd == "join"
	if deferred {
		if err := e.DeferCreateMessage(false); err != nil {
			b.log.Warn("defer", "err", err)
			return
		}
	}
	go func() {
		defer b.guard("command")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		b.respond(e, b.run(ctx, r, *g, cmd, data, user.ID, name, userCh, dj), deferred)
	}()
}

func (b *houseBot) respond(e *events.ApplicationCommandInteractionCreate, r reply, deferred bool) {
	var err error
	if deferred {
		_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{Content: &r.Text})
	} else {
		msg := discord.MessageCreate{Content: r.Text}
		if !r.Public {
			msg.Flags = discord.MessageFlagEphemeral
		}
		err = e.CreateMessage(msg, rest.WithCtx(context.Background()))
	}
	if err != nil {
		b.log.Warn("reply", "err", err)
	}
}

func (b *houseBot) run(ctx context.Context, r *room.Room, g snowflake.ID, cmd string, data discord.SlashCommandInteractionData, user snowflake.ID, name string, userCh snowflake.ID, dj bool) reply {
	ctrl := r.Controller()
	roomCh, roomName := r.Where()
	switch cmd {
	case "join":
		if rep, move := joinReply(roomCh, roomName, userCh, b.listeners(g, roomCh)); !move {
			return rep
		}
		chName := b.channelName(userCh)
		if err := r.Join(ctx, userCh, chName); err != nil {
			return reply{Text: "Couldn't join: " + err.Error()}
		}
		return reply{Text: "Playing in " + chName, Public: true}
	case "leave":
		r.Leave(ctx)
		return reply{Text: "Left " + roomName, Public: true}
	case "play":
		ts, label, refusal := b.req.Resolve(ctx, data.String("what"))
		if refusal != "" {
			return reply{Text: refusal}
		}
		n, err := r.Request(ctx, user.String(), name, ts)
		if errors.Is(err, audio.ErrTooManyPending) {
			return reply{Text: fmt.Sprintf("You have %d songs waiting; wait for some to play", n)}
		}
		if err != nil {
			return reply{Text: "Couldn't fetch that: " + err.Error()}
		}
		if len(ts) == 1 {
			return reply{Text: name + " added " + label, Public: true}
		}
		return reply{Text: fmt.Sprintf("%s added %s (%d songs)", name, label, len(ts)), Public: true}
	case "queue":
		count := 10
		if n, ok := data.OptInt("count"); ok {
			count = min(max(n, 1), 25)
		}
		return reply{Text: queueText(r, count), Public: data.Bool("public")}
	case "np":
		t, ok := ctrl.Current()
		if !ok {
			return reply{Text: "Nothing is playing"}
		}
		_, by := ctrl.RequesterOf(ctrl.Index())
		s := fmt.Sprintf("%s (%s / %s)", requests.TrackLabel(t), requests.Clock(r.Engine().Position()), requests.Clock(t.Duration))
		if by != "" {
			s += ", asked by " + by
		}
		return reply{Text: s}
	case "skip":
		msg, err := r.Skip(ctx, user.String(), name, dj)
		if err != nil {
			return reply{Text: "Couldn't skip: " + err.Error()}
		}
		return reply{Text: msg, Public: true}
	case "remove":
		return removeReply(r, data.String("what"), user.String(), dj)
	case "pause":
		ctrl.TogglePause()
		if r.Engine().Playing() {
			return reply{Text: "Resumed by " + name, Public: true}
		}
		return reply{Text: "Paused by " + name, Public: true}
	case "clear":
		n := ctrl.Clear()
		r.SetAutoplay(r.Autoplay()) // refill when autoplay is on
		if n > 0 {
			return reply{Text: fmt.Sprintf("Queue cleared (and %d requests)", n), Public: true}
		}
		return reply{Text: "Queue cleared", Public: true}
	case "autoplay":
		if data.String("mode") == "off" {
			r.SetAutoplay(false)
			return reply{Text: "Autoplay off for tonight", Public: true}
		}
		r.SetAutoplay(true)
		return reply{Text: "Autoplay on", Public: true}
	}
	return reply{Text: "Unknown command"}
}

func queueText(r *room.Room, count int) string {
	ctrl := r.Controller()
	ts, i := ctrl.Tracks(), ctrl.Index()
	var lines []string
	if t, ok := ctrl.Current(); ok {
		_, by := ctrl.RequesterOf(i)
		lines = append(lines, "Now: "+withName(requests.TrackLabel(t), by))
	}
	for j := i + 1; j < len(ts) && j <= i+count; j++ {
		_, by := ctrl.RequesterOf(j)
		lines = append(lines, fmt.Sprintf("%d. %s", j-i, withName(requests.TrackLabel(ts[j]), by)))
	}
	var waiting []string
	for _, l := range ctrl.Pending() {
		if len(l.Tracks) > 0 && l.Name != radio.RadioBy {
			waiting = append(waiting, fmt.Sprintf("%s %d", l.Name, len(l.Tracks)))
		}
	}
	if len(waiting) > 0 {
		lines = append(lines, "Waiting: "+strings.Join(waiting, ", "))
	}
	if len(lines) == 0 {
		return "The queue is empty"
	}
	return strings.Join(lines, "\n")
}

func withName(label, name string) string {
	if name == "" {
		return label
	}
	return label + " — " + name
}

// removeReply handles a /remove value r:<index>:<trackID> from autocomplete.
func removeReply(r *room.Room, what, user string, dj bool) reply {
	parts := strings.SplitN(what, ":", 3)
	if len(parts) != 3 || parts[0] != "r" {
		return reply{Text: "Pick a song from the list"}
	}
	i, err := strconv.Atoi(parts[1])
	ctrl := r.Controller()
	ts := ctrl.Tracks()
	if err != nil || i <= ctrl.Index() || i >= len(ts) || ts[i].ID != parts[2] {
		return reply{Text: "That song has moved or played"}
	}
	if by, _ := ctrl.RequesterOf(i); !dj && by != user {
		return reply{Text: "That's not your song"}
	}
	if err := ctrl.Remove(context.Background(), i); err != nil {
		return reply{Text: "Couldn't remove it: " + err.Error()}
	}
	return reply{Text: "Removed " + ts[i].Title}
}

func (b *houseBot) onAutocomplete(e *events.AutocompleteInteractionCreate) {
	g := e.GuildID()
	if g == nil || !allowedGuild(b.cfg, *g) || b.rooms[*g] == nil {
		return
	}
	r := b.rooms[*g]
	q := e.Data.String("what")
	cmd := e.Data.CommandName
	user := e.User()
	var roles []snowflake.ID
	if m := e.Member(); m != nil {
		roles = m.RoleIDs
	}
	dj := isDJ(roles, b.roleName(*g), user.ID.String(), b.cfg.OwnerID, b.cfg.DJRole)
	go func() {
		defer b.guard("autocomplete")
		var choices []discord.AutocompleteChoice
		if cmd == "remove" {
			choices = removeChoices(r, user.ID.String(), dj, q)
		} else {
			choices = b.req.Complete(context.Background(), q)
		}
		if err := e.AutocompleteResult(choices); err != nil {
			b.log.Warn("autocomplete", "err", err)
		}
	}()
}

func removeChoices(r *room.Room, user string, dj bool, q string) []discord.AutocompleteChoice {
	ctrl := r.Controller()
	ts, i := ctrl.Tracks(), ctrl.Index()
	var out []discord.AutocompleteChoice
	for j := i + 1; j < len(ts) && len(out) < 25; j++ {
		by, _ := ctrl.RequesterOf(j)
		if !dj && by != user {
			continue
		}
		label := requests.TrackLabel(ts[j])
		if q != "" && !strings.Contains(strings.ToLower(label), strings.ToLower(q)) {
			continue
		}
		out = append(out, discord.AutocompleteChoiceString{Name: cut(label, 100), Value: fmt.Sprintf("r:%d:%s", j, ts[j].ID)})
	}
	return out
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
