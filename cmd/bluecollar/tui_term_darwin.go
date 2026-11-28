//go:build darwin || freebsd || openbsd || netbsd

package main

import "golang.org/x/sys/unix"

const (
	termiosReadRequest  = unix.TIOCGETA
	termiosWriteRequest = unix.TIOCSETA
)
