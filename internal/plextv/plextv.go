// Package plextv signs in with a Plex account and finds the account's servers.
package plextv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brady1408/dotamp/internal/netlog"
)

const (
	product = "dotamp"
	version = "0.1.0"
)

type Client struct {
	BaseURL  string // https://plex.tv; tests point it elsewhere
	clientID string
	http     *http.Client
}

func New(clientID string) *Client {
	return &Client{
		BaseURL:  "https://plex.tv",
		clientID: clientID,
		http:     &http.Client{Timeout: 20 * time.Second, Transport: netlog.New()},
	}
}

func (c *Client) headers(h http.Header, token string) {
	h.Set("X-Plex-Client-Identifier", c.clientID)
	h.Set("X-Plex-Product", product)
	h.Set("X-Plex-Version", version)
	h.Set("X-Plex-Platform", "terminal")
	h.Set("Accept", "application/json")
	if token != "" {
		h.Set("X-Plex-Token", token)
	}
}

func (c *Client) do(ctx context.Context, method, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	c.headers(req.Header, token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("plex.tv: %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// Pin is a sign-in request the user approves at AuthURL.
type Pin struct {
	ID   int    `json:"id"`
	Code string `json:"code"`
}

type pinStatus struct {
	Pin
	AuthToken string `json:"authToken"`
}

func (c *Client) RequestPin(ctx context.Context) (Pin, error) {
	var p Pin
	if err := c.do(ctx, http.MethodPost, "/api/v2/pins?strong=true", "", &p); err != nil {
		return Pin{}, err
	}
	if p.ID == 0 || p.Code == "" {
		return Pin{}, errors.New("plex.tv: empty pin")
	}
	return p, nil
}

// AuthURL is where the user approves the pin; the code is pre-filled.
func (c *Client) AuthURL(p Pin) string {
	q := url.Values{
		"clientID":                 {c.clientID},
		"code":                     {p.Code},
		"context[device][product]": {product},
	}
	return "https://app.plex.tv/auth#?" + q.Encode()
}

// PollPin waits for the pin to be approved and returns the account token.
func (c *Client) PollPin(ctx context.Context, p Pin, every time.Duration) (string, error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		var st pinStatus
		if err := c.do(ctx, http.MethodGet, "/api/v2/pins/"+strconv.Itoa(p.ID), "", &st); err != nil {
			return "", err
		}
		if st.AuthToken != "" {
			return st.AuthToken, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t.C:
		}
	}
}

// Server is a media server the account can reach, with every way to reach it.
type Server struct {
	Name        string
	ID          string
	Owned       bool
	AccessToken string
	Connections []Connection
}

type Connection struct {
	URI   string
	Local bool
	Relay bool
}

type resource struct {
	Name             string `json:"name"`
	Provides         string `json:"provides"`
	ClientIdentifier string `json:"clientIdentifier"`
	Owned            bool   `json:"owned"`
	AccessToken      string `json:"accessToken"`
	Connections      []struct {
		URI   string `json:"uri"`
		Local bool   `json:"local"`
		Relay bool   `json:"relay"`
	} `json:"connections"`
}

// Servers lists the account's media servers.
func (c *Client) Servers(ctx context.Context, token string) ([]Server, error) {
	var rs []resource
	if err := c.do(ctx, http.MethodGet, "/api/v2/resources?includeHttps=1&includeRelay=1", token, &rs); err != nil {
		return nil, err
	}
	var out []Server
	for _, r := range rs {
		if !strings.Contains(r.Provides, "server") {
			continue
		}
		s := Server{Name: r.Name, ID: r.ClientIdentifier, Owned: r.Owned, AccessToken: r.AccessToken}
		for _, cn := range r.Connections {
			s.Connections = append(s.Connections, Connection{URI: cn.URI, Local: cn.Local, Relay: cn.Relay})
		}
		out = append(out, s)
	}
	return out, nil
}

// ChooseServer picks the server named name, or the first owned one when name
// is empty. It returns nil when nothing matches.
func ChooseServer(servers []Server, name string) *Server {
	for i := range servers {
		if name != "" && servers[i].Name == name {
			return &servers[i]
		}
	}
	if name != "" {
		return nil
	}
	for i := range servers {
		if servers[i].Owned {
			return &servers[i]
		}
	}
	if len(servers) > 0 {
		return &servers[0]
	}
	return nil
}

// Probe returns a check that a connection answers /identity with the token.
func Probe(token, clientID string) func(context.Context, Connection) error {
	client := &http.Client{Timeout: 4 * time.Second, Transport: netlog.New()}
	return func(ctx context.Context, cn Connection) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cn.URI+"/identity", nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Plex-Token", token)
		req.Header.Set("X-Plex-Client-Identifier", clientID)
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return nil
	}
}

// Connect picks the best reachable connection: local first (plain http
// before plex.direct names, which some LAN resolvers refuse), then the
// public address, then Plex's relay. Each tier is probed in parallel and the
// next tier is tried only when the whole tier fails.
func Connect(ctx context.Context, s Server, probe func(context.Context, Connection) error) (Connection, error) {
	tiers := [3][]Connection{}
	for _, cn := range s.Connections {
		switch {
		case cn.Relay:
			tiers[2] = append(tiers[2], cn)
		case cn.Local:
			if strings.HasPrefix(cn.URI, "http://") {
				tiers[0] = append([]Connection{cn}, tiers[0]...)
			} else {
				tiers[0] = append(tiers[0], cn)
			}
		default:
			tiers[1] = append(tiers[1], cn)
		}
	}
	for _, tier := range tiers {
		if len(tier) == 0 {
			continue
		}
		if cn, ok := firstReachable(ctx, tier, probe); ok {
			return cn, nil
		}
	}
	return Connection{}, fmt.Errorf("plex.tv: no connection to %s answered", s.Name)
}

// firstReachable probes a tier in parallel and returns the earliest listed
// connection that answered.
func firstReachable(ctx context.Context, tier []Connection, probe func(context.Context, Connection) error) (Connection, bool) {
	ok := make([]bool, len(tier))
	var wg sync.WaitGroup
	for i, cn := range tier {
		wg.Add(1)
		go func(i int, cn Connection) {
			defer wg.Done()
			ok[i] = probe(ctx, cn) == nil
		}(i, cn)
	}
	wg.Wait()
	for i, cn := range tier {
		if ok[i] {
			return cn, true
		}
	}
	return Connection{}, false
}
