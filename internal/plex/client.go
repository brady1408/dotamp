package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const Version = "0.1.0"

type Client struct {
	server   string
	token    string
	clientID string
	section  string
	http     *http.Client
}

func New(server, token, clientID string) *Client {
	return &Client{
		server:   strings.TrimRight(server, "/"),
		token:    token,
		clientID: clientID,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) SetSection(id string) { c.section = id }

func (c *Client) headers(h http.Header) {
	h.Set("X-Plex-Token", c.token)
	h.Set("X-Plex-Client-Identifier", c.clientID)
	h.Set("X-Plex-Product", "dotamp")
	h.Set("X-Plex-Version", Version)
	h.Set("Accept", "application/json")
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out *container) error {
	u := c.server + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	c.headers(req.Header)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plex: %s: HTTP %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// StreamHeaders are the headers the audio layer must send on a stream URL.
func (c *Client) StreamHeaders() map[string]string {
	h := http.Header{}
	c.headers(h)
	m := map[string]string{}
	for k := range h {
		m[k] = h.Get(k)
	}
	return m
}
