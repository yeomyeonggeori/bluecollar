//go:build unix

package main

import (
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

type terminalState struct {
	termios *unix.Termios
}

func enterRawTerminal(fileDescriptor int) (terminalState, error) {
	original, errorValue := unix.IoctlGetTermios(fileDescriptor, termiosReadRequest)
	if errorValue != nil {
		return terminalState{}, errorValue
	}
	raw := *original
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if setError := unix.IoctlSetTermios(fileDescriptor, termiosWriteRequest, &raw); setError != nil {
		return terminalState{}, setError
	}
	return terminalState{termios: original}, nil
}

func restoreTerminal(fileDescriptor int, state terminalState) {
	if state.termios == nil {
		return
	}
	_ = unix.IoctlSetTermios(fileDescriptor, termiosWriteRequest, state.termios)
}

func terminalWindowSize(fileDescriptor int) (int, int) {
	windowSize, errorValue := unix.IoctlGetWinsize(fileDescriptor, unix.TIOCGWINSZ)
	if errorValue != nil || windowSize == nil || windowSize.Col == 0 || windowSize.Row == 0 {
		return 80, 24
	}
	return int(windowSize.Col), int(windowSize.Row)
}

func isTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	_, errorValue := unix.IoctlGetTermios(int(file.Fd()), termiosReadRequest)
	return errorValue == nil
}

func subscribeWindowResize(channel chan<- os.Signal) {
	signal.Notify(channel, unix.SIGWINCH)
}
