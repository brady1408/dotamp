package main

import (
	"errors"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

func TestWebSongsMarkTheLinedUpOne(t *testing.T) {
	ts := []library.Track{{ID: "1", Title: "Shine", Artist: "Collective Soul"}, {ID: "2", Title: "Heavy"}}
	got := webSongs(ts, true)
	if len(got) != 2 || !got[0].Next || got[1].Next || got[0].Artist != "Collective Soul" {
		t.Fatalf("fed: %+v", got)
	}
	if got := webSongs(ts, false); got[0].Next {
		t.Fatalf("not fed: %+v", got)
	}
	if got := webSongs(nil, false); got == nil || len(got) != 0 {
		t.Fatalf("empty must be a list: %#v", got)
	}
}

func TestWebChoicesKeepDiscordsLabelsAndValues(t *testing.T) {
	cs := webChoices([]discord.AutocompleteChoice{discord.AutocompleteChoiceString{Name: "♪ Shine — Collective Soul", Value: "t:1"}})
	if len(cs) != 1 || cs[0].Label != "♪ Shine — Collective Soul" || cs[0].Value != "t:1" {
		t.Fatalf("choices = %+v", cs)
	}
}

func TestWebAddMessages(t *testing.T) {
	if got, err := addResult("Shine", 1, 3, nil); err != nil || got != "Added Shine" {
		t.Fatalf("one: %q %v", got, err)
	}
	if got, _ := addResult("Dosage", 11, 11, nil); got != "Added Dosage (11 songs)" {
		t.Fatalf("album: %q", got)
	}
	if _, err := addResult("Dosage", 11, 4, audio.ErrTooManyPending); err == nil || err.Error() != "You have 4 songs waiting; wait for some to play" {
		t.Fatalf("cap: %v", err)
	}
	if _, err := addResult("x", 1, 0, errors.New("boom")); err == nil || err.Error() != "Couldn't fetch that: boom" {
		t.Fatalf("fail: %v", err)
	}
}

func TestWebLaneErrorsReadAsSentences(t *testing.T) {
	if err := laneErr(audio.ErrMoved); err == nil || err.Error() != "That song has moved or played" {
		t.Fatalf("moved: %v", err)
	}
	if laneErr(nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
