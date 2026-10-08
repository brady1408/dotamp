package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/multi"
)

// twoServers builds a multi library over two stub servers.
func twoServers() *multi.Library {
	m := multi.New()
	m.Add(multi.Server{ID: "A", Name: "Ressikan", Owned: true, Via: "local"}, stubLib{})
	m.Add(multi.Server{ID: "B", Name: "Friend", Via: "relay"}, stubLib{})
	return m
}

func TestRootMenuListsServersAndSwitches(t *testing.T) {
	m := twoServers()
	b := NewBrowser(m)
	b.SetSwitcher(m)
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	var menu []string
	for _, r := range b.List().Rows {
		menu = append(menu, r.Menu)
	}
	if strings.Join(menu, ",") != MenuArtists+","+MenuRecent+","+MenuPlaylists+","+MenuServers {
		t.Fatalf("root menu = %v", menu)
	}
	if err := b.OpenServers(ctx); err != nil {
		t.Fatal(err)
	}
	rows := b.List().Rows
	if len(rows) != 3 || rows[1].ServerID != "A" || !strings.Contains(rows[1].Text, "Ressikan") || !strings.Contains(rows[1].Text, "local") || rows[2].ServerID != "B" {
		t.Fatalf("server rows = %+v", rows)
	}
	if !strings.HasPrefix(rows[1].Text, "▸") || strings.HasPrefix(rows[2].Text, "▸") {
		t.Fatalf("the current server should be marked: %q / %q", rows[1].Text, rows[2].Text)
	}
	b.List().Move(1)
	if !b.SwitchServer(ctx, b.List().Selected().ServerID) || m.Current().ID != "B" || b.Title() != "Library" {
		t.Fatalf("switching should set the current server and return to the root; current=%s title=%q", m.Current().ID, b.Title())
	}
}

func TestSingleServerHidesTheMenuEntry(t *testing.T) {
	m := multi.New()
	m.Add(multi.Server{ID: "A", Name: "Only"}, stubLib{})
	b := NewBrowser(m)
	b.SetSwitcher(m)
	_ = b.LoadRoot(context.Background())
	for _, r := range b.List().Rows {
		if r.Menu == MenuServers {
			t.Fatal("one server needs no Servers entry")
		}
	}
}

func TestSearchGroupsByServerWhenThereAreSeveral(t *testing.T) {
	m := twoServers()
	b := NewBrowser(m)
	b.SetSwitcher(m)
	if err := b.Search(context.Background(), "nsync"); err != nil {
		t.Fatal(err)
	}
	var headers []string
	for _, r := range b.List().Rows {
		if r.Header {
			headers = append(headers, r.Text)
		}
	}
	joined := strings.Join(headers, "|")
	if !strings.Contains(joined, "Ressikan") || !strings.Contains(joined, "Friend (relay)") {
		t.Fatalf("headers should name the servers: %v", headers)
	}
	// Both servers returned the same album; both copies are listed under their own header.
	albums := 0
	for _, r := range b.List().Rows {
		if r.Album != nil {
			albums++
		}
	}
	if albums != 2 {
		t.Fatalf("albums = %d, want one per server", albums)
	}
}

func TestAppTitleShowsCurrentServer(t *testing.T) {
	app, s, _ := newApp(t)
	m := twoServers()
	app.browser = NewBrowser(m)
	app.browser.SetSwitcher(m)
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	app.Draw()
	if r := rows(s)[10]; !strings.Contains(r, "Ressikan") {
		t.Fatalf("tab row should name the current server: %q", r)
	}
	_ = library.Track{} // keep the import honest if stubs change
}

func TestRootMenuPicksUpServersThatConnectLater(t *testing.T) {
	m := multi.New()
	m.Add(multi.Server{ID: "A", Name: "Mine"}, stubLib{})
	b := NewBrowser(m)
	b.SetSwitcher(m)
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	if len(b.List().Rows) != 3 {
		t.Fatalf("one server: %d rows", len(b.List().Rows))
	}
	m.Add(multi.Server{ID: "B", Name: "Friend"}, stubLib{}) // a background connect finished
	b.RefreshRoot(ctx)
	if len(b.List().Rows) != 4 || b.List().Rows[3].Menu != MenuServers {
		t.Fatalf("root should now offer Servers: %+v", b.List().Rows)
	}
	b.List().Sel = 1
	b.RefreshRoot(ctx)
	if b.List().Sel != 1 {
		t.Fatal("a refresh with no change must not move the selection")
	}
	_ = b.OpenArtists(ctx)
	b.RefreshRoot(ctx) // not at the root: no-op
	if b.Title() == "Library" {
		t.Fatal("refresh must not pop a deeper view")
	}
}
