//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// drainStdin discards whatever is queued in the console input buffer.
//
// The Windows counterpart of the TIOCFLUSH ioctl the Unix builds use, and it exists
// for the same reason: an apply takes minutes with nothing to print, and an Enter
// pressed while waiting is still queued when the confirmation finally appears.
// Without this, that keystroke answers a question nobody read.
//
// It is not the generic fallback: drain_other.go fails closed, which would make
// every interactive apply and destroy on Windows impossible without --auto-approve —
// and .goreleaser.yml ships a Windows binary.
func drainStdin() error {
	handle := windows.Handle(os.Stdin.Fd())
	if err := windows.FlushConsoleInputBuffer(handle); err != nil {
		return fmt.Errorf("cannot flush the console input buffer: %w", err)
	}
	return nil
}
