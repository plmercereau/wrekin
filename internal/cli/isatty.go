package cli

import (
	"syscall"
	"unsafe"
)

// isTerminal reports whether fd is a terminal: the terminal-attributes ioctl
// succeeds on a TTY only. A character-device mode bit is not enough, since
// /dev/null is one too.
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
