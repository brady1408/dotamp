//go:build !darwin

package mediakeys

// RunMain runs f; only macOS needs the main thread for a run loop.
func RunMain(f func() error) error { return f() }
