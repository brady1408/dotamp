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

	"github.com/brady1408/dotamp/internal/netlog"
)

const Version = "0.1.0"

type Client struct {
	server   string
	token    string
	clientID string
	section  string
	http     *http.Client

	serverID, serverName string // tag on every item, for multi-server routing
	local, relay         bool   // how the connection to this server was reached
	relayCap             int    // kbps the relay can carry; 0 = unknown
	remoteBitrate        int    // kbps to transcode to on any non-local connection; 0 = original
	machineID            string // from /identity, fetched once; playlists are addressed through it
}

func New(server, token, clientID string) *Client {
	return &Client{
		server:   strings.TrimRight(server, "/"),
		token:    token,
		clientID: clientID,
		http:     &http.Client{Timeout: 30 * time.Second, Transport: netlog.New()},
	}
}

func (c *Client) SetSection(id string) { c.section = id }

// SetServer names the server these items come from.
func (c *Client) SetServer(id, name string) { c.serverID, c.serverName = id, name }

// SetConnection records how the server was reached. A relay carries at most
// relayCap kbps, so files above it are transcoded.
func (c *Client) SetConnection(local, relay bool, relayCap int) {
	c.local, c.relay, c.relayCap = local, relay, relayCap
}

// SetRemoteBitrate forces transcoding to kbps on any non-local connection.
func (c *Client) SetRemoteBitrate(kbps int) { c.remoteBitrate = kbps }

func (c *Client) headers(h http.Header) {
	h.Set("X-Plex-Token", c.token)
	h.Set("X-Plex-Client-Identifier", c.clientID)
	h.Set("X-Plex-Product", "dotamp")
	h.Set("X-Plex-Version", Version)
	h.Set("Accept", "application/json")
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out *container) error {
	return c.do(ctx, http.MethodGet, path, q, out)
}

func (c *Client) post(ctx context.Context, path string, q url.Values, out *container) error {
	return c.do(ctx, http.MethodPost, path, q, out)
}

func (c *Client) put(ctx context.Context, path string, q url.Values, out *container) error {
	return c.do(ctx, http.MethodPut, path, q, out)
}

func (c *Client) delete(ctx context.Context, path string, q url.Values, out *container) error {
	return c.do(ctx, http.MethodDelete, path, q, out)
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, out *container) error {
	u := c.server + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	c.headers(req.Header)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("plex: %s: HTTP %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
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
