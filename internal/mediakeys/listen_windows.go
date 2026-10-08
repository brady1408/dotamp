//go:build windows

package mediakeys

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	registerHotKey    = user32.NewProc("RegisterHotKey")
	unregisterHotKey  = user32.NewProc("UnregisterHotKey")
	getMessage        = user32.NewProc("GetMessageW")
	postThreadMessage = user32.NewProc("PostThreadMessageW")
	getCurrentThread  = kernel32.NewProc("GetCurrentThreadId")
)

const (
	wmHotkey = 0x0312
	wmQuit   = 0x0012
)

// msg mirrors the Win32 MSG struct.
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

// listen registers each media key as a global hotkey on a dedicated OS
// thread (hotkeys belong to the thread that registers them) and pumps that
// thread's message queue. A key another program already holds is skipped
// with a log line; the rest still work.
func listen(ctx context.Context, h Handler) error {
	keys := []uint32{vkMediaPlayPause, vkMediaNextTrack, vkMediaPrevTrack}
	tid := make(chan uintptr, 1)
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		id, _, _ := getCurrentThread.Call()
		tid <- id
		registered := 0
		for i, vk := range keys {
			r, _, err := registerHotKey.Call(0, uintptr(i+1), 0, uintptr(vk))
			if r == 0 {
				log.Printf("media keys: %s not registered: %v", actionFor(vk), err)
				continue
			}
			registered++
		}
		if registered == 0 {
			done <- fmt.Errorf("media keys: none could be registered")
			return
		}
		log.Printf("media keys: %d of %d registered", registered, len(keys))
		var m msg
		for {
			r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if r == 0 || int32(r) == -1 { // WM_QUIT or error
				break
			}
			if m.message == wmHotkey {
				idx := int(m.wParam) - 1
				if idx >= 0 && idx < len(keys) {
					h(actionFor(keys[idx]))
				}
			}
		}
		for i := range keys {
			_, _, _ = unregisterHotKey.Call(0, uintptr(i+1))
		}
		done <- nil
	}()
	id := <-tid
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_, _, _ = postThreadMessage.Call(id, wmQuit, 0, 0)
		return <-done
	}
}
