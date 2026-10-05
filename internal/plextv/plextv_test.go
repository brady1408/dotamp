package plextv

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fakePlexTV(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var polls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/pins", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("strong") != "true" || r.Header.Get("X-Plex-Client-Identifier") != "cid" ||
			r.Header.Get("X-Plex-Product") != "dotamp" || r.Header.Get("Accept") != "application/json" {
			http.Error(w, "bad request", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 4242, "code": "ABCD"})
	})
	mux.HandleFunc("GET /api/v2/pins/4242", func(w http.ResponseWriter, r *http.Request) {
		n := polls.Add(1)
		tok := ""
		if n >= 2 { // approved on the second poll
			tok = "account-token"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 4242, "code": "ABCD", "authToken": tok})
	})
	mux.HandleFunc("GET /api/v2/resources", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "account-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Query().Get("includeHttps") != "1" || r.URL.Query().Get("includeRelay") != "1" {
			http.Error(w, "missing include flags", 400)
			return
		}
		b, _ := os.ReadFile("testdata/resources.json")
		_, _ = w.Write(b)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s, &polls
}

func TestPinFlow(t *testing.T) {
	s, polls := fakePlexTV(t)
	c := New("cid")
	c.BaseURL = s.URL
	pin, err := c.RequestPin(context.Background())
	if err != nil || pin.ID != 4242 || pin.Code != "ABCD" {
		t.Fatalf("pin=%+v err=%v", pin, err)
	}
	u := c.AuthURL(pin)
	if !strings.HasPrefix(u, "https://app.plex.tv/auth#?") || !strings.Contains(u, "clientID=cid") ||
		!strings.Contains(u, "code=ABCD") || !strings.Contains(u, "product%5D=dotamp") {
		t.Fatalf("auth url = %s", u)
	}
	tok, err := c.PollPin(context.Background(), pin, 10*time.Millisecond)
	if err != nil || tok != "account-token" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	if polls.Load() != 2 {
		t.Fatalf("polls = %d", polls.Load())
	}
}

func TestPollPinStopsOnContext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/pins/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "code": "X", "authToken": ""})
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	c := New("cid")
	c.BaseURL = s.URL
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := c.PollPin(ctx, Pin{ID: 1, Code: "X"}, 10*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestServersFromResources(t *testing.T) {
	s, _ := fakePlexTV(t)
	c := New("cid")
	c.BaseURL = s.URL
	servers, err := c.Servers(context.Background(), "account-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].Name != "m80q" || !servers[0].Owned || servers[0].AccessToken != "srv-token" {
		t.Fatalf("servers = %+v", servers)
	}
	if n := len(servers[0].Connections); n != 4 {
		t.Fatalf("connections = %d", n)
	}
	if _, err := c.Servers(context.Background(), "wrong"); err == nil {
		t.Fatal("expected an auth error")
	}
}

func TestChooseServerPrefersNamedThenOwned(t *testing.T) {
	servers := []Server{{Name: "friend", Owned: false}, {Name: "m80q", Owned: true}, {Name: "spare", Owned: true}}
	if got := ChooseServer(servers, ""); got == nil || got.Name != "m80q" {
		t.Fatalf("default pick = %+v", got)
	}
	if got := ChooseServer(servers, "spare"); got == nil || got.Name != "spare" {
		t.Fatalf("named pick = %+v", got)
	}
	if got := ChooseServer(servers, "nope"); got != nil {
		t.Fatalf("unknown name should pick nothing, got %+v", got)
	}
}

func TestConnectPrefersLocalThenRemoteThenRelay(t *testing.T) {
	s, _ := fakePlexTV(t)
	c := New("cid")
	c.BaseURL = s.URL
	servers, _ := c.Servers(context.Background(), "account-token")
	srv := servers[0]
	reachable := func(ok ...string) func(context.Context, Connection) error {
		return func(_ context.Context, cn Connection) error {
			for _, u := range ok {
				if cn.URI == u {
					return nil
				}
			}
			return errors.New("unreachable")
		}
	}
	// At home: the plain http LAN address wins over the https plex.direct one.
	got, err := Connect(context.Background(), srv, reachable("http://192.168.1.10:32400", "https://192-168-1-10.abc.plex.direct:32400", "https://203-0-113-7.abc.plex.direct:32400"))
	if err != nil || got.URI != "http://192.168.1.10:32400" {
		t.Fatalf("home = %+v err=%v", got, err)
	}
	// Away: local fails, the public address wins over the relay.
	got, err = Connect(context.Background(), srv, reachable("https://203-0-113-7.abc.plex.direct:32400", "https://abc.relay.plex.direct:8443"))
	if err != nil || got.URI != "https://203-0-113-7.abc.plex.direct:32400" {
		t.Fatalf("away = %+v err=%v", got, err)
	}
	// Only the relay answers.
	got, err = Connect(context.Background(), srv, reachable("https://abc.relay.plex.direct:8443"))
	if err != nil || !got.Relay {
		t.Fatalf("relay = %+v err=%v", got, err)
	}
	if _, err := Connect(context.Background(), srv, reachable()); err == nil {
		t.Fatal("nothing reachable must be an error")
	}
}

func TestProbeIdentity(t *testing.T) {
	var seenToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.Header.Get("X-Plex-Token")
		if r.URL.Path != "/identity" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"srv1"}}`))
	}))
	defer srv.Close()
	p := Probe("srv-token", "cid")
	if err := p(context.Background(), Connection{URI: srv.URL}); err != nil || seenToken != "srv-token" {
		t.Fatalf("err=%v token=%q", err, seenToken)
	}
	srv.Close()
	if err := p(context.Background(), Connection{URI: srv.URL}); err == nil {
		t.Fatal("a closed server must fail the probe")
	}
}

func TestSubscribed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "account-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"username":"brady","subscription":{"active":true,"status":"Active"}}`))
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	c := New("cid")
	c.BaseURL = s.URL
	ok, err := c.Subscribed(context.Background(), "account-token")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	u, err := c.User(context.Background(), "account-token")
	if err != nil || u.Username != "brady" || !u.Subscribed {
		t.Fatalf("user=%+v err=%v", u, err)
	}
	if _, err := c.Subscribed(context.Background(), "bad"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDiagnoseReportsEveryConnection(t *testing.T) {
	srv := Server{Name: "x", Connections: []Connection{{URI: "a", Local: true}, {URI: "b"}, {URI: "c", Relay: true}}}
	probe := func(_ context.Context, cn Connection) error {
		if cn.URI == "b" {
			return nil
		}
		return errors.New("nope")
	}
	out := Diagnose(context.Background(), srv, probe)
	if len(out) != 3 || out[0].Err == nil || out[1].Err != nil || out[2].Err == nil || out[1].URI != "b" {
		t.Fatalf("outcomes = %+v", out)
	}
}
