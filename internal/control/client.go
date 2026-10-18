package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

var ErrNotRunning = errors.New("dotamp is not running")

// Client makes one call at a time to the player named by control.json.
type Client struct{ path string }

func NewClient(path string) *Client { return &Client{path: path} }

// Call sends req with the published token and returns the reply. A missing
// file or a dead port is ErrNotRunning.
func (c *Client) Call(ctx context.Context, req Request) (Reply, error) {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return Reply{}, ErrNotRunning
	}
	var f struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	if json.Unmarshal(b, &f) != nil || f.Port == 0 {
		return Reply{}, ErrNotRunning
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", f.Port))
	if err != nil {
		return Reply{}, ErrNotRunning
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(callTimeout + 5*time.Second))
	}
	req.Token = f.Token
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Reply{}, err
	}
	var r Reply
	if err := json.NewDecoder(conn).Decode(&r); err != nil {
		return Reply{}, err
	}
	return r, nil
}
