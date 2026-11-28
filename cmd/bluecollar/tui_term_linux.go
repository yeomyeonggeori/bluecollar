//go:build linux

package main

import "golang.org/x/sys/unix"

const (
	termiosReadRequest  = unix.TCGETS
	termiosWriteRequest = unix.TCSETS
)
