//go:build linux

package mediakeys

import (
	"context"
	"os"
)

func listen(ctx context.Context, p Player) error {
	return listenMPRIS(ctx, p, os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
}
