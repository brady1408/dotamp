//go:build !windows && !linux

package mediakeys

import "context"

func listen(context.Context, Player) error { return nil }
