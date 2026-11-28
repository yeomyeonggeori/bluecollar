//go:build !unix

package main

import (
	"errors"
	"os"
)

type terminalState struct{}

func enterRawTerminal(int) (terminalState, error) {
	return terminalState{}, errors.New("raw terminal mode is unsupported on this platform")
}

func restoreTerminal(int, terminalState) {}

func terminalWindowSize(int) (int, int) {
	return 80, 24
}

func isTerminalFile(*os.File) bool {
	return false
}

func subscribeWindowResize(chan<- os.Signal) {}
