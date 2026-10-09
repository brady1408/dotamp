//go:build darwin

package mediakeys

import (
	"time"

	"github.com/ebitengine/purego"
)

const frameworkCoreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"

// RunMain runs f on another goroutine while the calling thread, which must
// be the process's main thread, spins the Core Foundation run loop that
// macOS delivers remote commands on. It returns f's result once f is done
// and the loop has been stopped.
func RunMain(f func() error) error {
	cf, err := purego.Dlopen(frameworkCoreFoundation, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return f() // no run loop: the player still works, the keys do not
	}
	var runLoopRun func()
	var runLoopGetMain func() uintptr
	var runLoopStop func(uintptr)
	purego.RegisterLibFunc(&runLoopRun, cf, "CFRunLoopRun")
	purego.RegisterLibFunc(&runLoopGetMain, cf, "CFRunLoopGetMain")
	purego.RegisterLibFunc(&runLoopStop, cf, "CFRunLoopStop")
	done := make(chan error, 1)
	loop := runLoopGetMain()
	go func() {
		done <- f()
		runLoopStop(loop)
	}()
	for {
		runLoopRun() // returns when stopped, or at once when there is nothing scheduled yet
		select {
		case err := <-done:
			return err
		default:
			// nothing scheduled kept the loop from blocking; keep spinning without burning a core
			select {
			case err := <-done:
				return err
			case <-sleep():
			}
		}
	}
}

func sleep() <-chan time.Time { return time.After(50 * time.Millisecond) }
