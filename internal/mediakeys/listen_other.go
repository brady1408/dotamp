//go:build !windows

package mediakeys

import "context"

func listen(context.Context, Handler) error { return nil }
