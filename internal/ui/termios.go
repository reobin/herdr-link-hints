package ui

import (
	"os"
	"syscall"
	"unsafe"
)

// ioctl is the whole reason this file exists: the three stty calls Open
// used to make cost a fork each, against well under a microsecond here.
func ioctl(tty *os.File, request uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), request, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// rawMode reproduces `stty raw -echo`. keys.go reads ctrl-c and ctrl-d as
// bytes rather than signals, so ISIG has to go with the rest.
func rawMode(saved syscall.Termios) syscall.Termios {
	raw := saved
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	return raw
}

func enterRawIoctl(tty *os.File) (func(), error) {
	var saved syscall.Termios
	if err := ioctl(tty, getTermios, unsafe.Pointer(&saved)); err != nil {
		return nil, err
	}
	raw := rawMode(saved)
	if err := ioctl(tty, setTermios, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return func() { _ = ioctl(tty, setTermios, unsafe.Pointer(&saved)) }, nil
}

type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

func screenSizeIoctl(tty *os.File) (rows, cols int, ok bool) {
	var ws winsize
	if err := ioctl(tty, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, false
	}
	if ws.rows == 0 || ws.cols == 0 {
		return 0, 0, false
	}
	return int(ws.rows), int(ws.cols), true
}
