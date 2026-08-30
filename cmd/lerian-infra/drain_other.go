//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package main

import "errors"

// drainStdin cannot discard queued terminal input on this platform.
//
// It fails closed. The queue this exists to clear is what makes a stray Enter typed
// during a sixteen-minute apply answer the confirmation that follows it, and a
// queued "yes" would approve a destroy nobody read. Refusing to accept the
// confirmation is worse than a prompt and better than an unread approval — the
// operator can still pass --auto-approve, which is an explicit decision rather than
// an accident of typing.
func drainStdin() error {
	return errors.New("this platform has no way to discard queued terminal input")
}
