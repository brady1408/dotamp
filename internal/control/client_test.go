package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestClientReportsNotRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.json")
	if _, err := NewClient(path).Call(context.Background(), Request{Op: "status"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("missing file: %v", err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close() // nothing listens here now
	os.WriteFile(path, []byte(fmt.Sprintf(`{"port":%d,"token":"x"}`, p)), 0o600)
	if _, err := NewClient(path).Call(context.Background(), Request{Op: "status"}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("dead port: %v", err)
	}
}
