//go:build !windows && !linux && !darwin

package mediakeys

import "context"

func listen(context.Context, Player) error { return nil }
