//go:build darwin

package main

import "runtime"

// macOS delivers media remote commands on the main thread's run loop, so
// main must stay on the main OS thread for mediakeys.RunMain.
func init() { runtime.LockOSThread() }
