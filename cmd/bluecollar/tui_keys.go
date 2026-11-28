package main

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

type keyKind int

const (
	keyIgnore keyKind = iota
	keyRune
	keyPaste
	keyEnter
	keyShiftEnter
	keyAltEnter
	keyBackspace
	keyDelete
	keyTab
	keyEscape
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyScrollUp
	keyScrollDown
	keyCtrlA
	keyCtrlC
	keyCtrlD
	keyCtrlE
	keyCtrlJ
	keyCtrlK
	keyCtrlL
	keyCtrlO
	keyCtrlU
	keyCtrlW
)

type keyEvent struct {
	kind keyKind
	char rune
	text string
}

type keyReader struct {
	reader *bufio.Reader
}

func newKeyReader(source io.Reader) *keyReader {
	return &keyReader{reader: bufio.NewReaderSize(source, 4096)}
}

func (reader *keyReader) readKey() (keyEvent, error) {
	firstByte, errorValue := reader.reader.ReadByte()
	if errorValue != nil {
		return keyEvent{}, errorValue
	}
	switch firstByte {
	case 0x01:
		return keyEvent{kind: keyCtrlA}, nil
	case 0x03:
		return keyEvent{kind: keyCtrlC}, nil
	case 0x04:
		return keyEvent{kind: keyCtrlD}, nil
	case 0x05:
		return keyEvent{kind: keyCtrlE}, nil
	case 0x08, 0x7f:
		return keyEvent{kind: keyBackspace}, nil
	case 0x09:
		return keyEvent{kind: keyTab}, nil
	case 0x0a:
		return keyEvent{kind: keyCtrlJ}, nil
	case 0x0b:
		return keyEvent{kind: keyCtrlK}, nil
	case 0x0c:
		return keyEvent{kind: keyCtrlL}, nil
	case 0x0d:
		return keyEvent{kind: keyEnter}, nil
	case 0x0f:
		return keyEvent{kind: keyCtrlO}, nil
	case 0x15:
		return keyEvent{kind: keyCtrlU}, nil
	case 0x17:
		return keyEvent{kind: keyCtrlW}, nil
	case 0x1b:
		return reader.readEscapeSequence()
	}
	if firstByte < 0x20 {
		return reader.readKey()
	}
	return reader.readUTF8Rune(firstByte)
}

func (reader *keyReader) readUTF8Rune(firstByte byte) (keyEvent, error) {
	buffer := [utf8.UTFMax]byte{firstByte}
	length := 1
	for !utf8.FullRune(buffer[:length]) && length < utf8.UTFMax {
		nextByte, errorValue := reader.reader.ReadByte()
		if errorValue != nil {
			return keyEvent{}, errorValue
		}
		buffer[length] = nextByte
		length++
	}
	decoded, _ := utf8.DecodeRune(buffer[:length])
	return keyEvent{kind: keyRune, char: decoded}, nil
}

func (reader *keyReader) readEscapeSequence() (keyEvent, error) {
	if reader.reader.Buffered() == 0 {
		return keyEvent{kind: keyEscape}, nil
	}
	nextByte, errorValue := reader.reader.ReadByte()
	if errorValue != nil {
		return keyEvent{kind: keyEscape}, nil
	}
	if nextByte == 0x0d || nextByte == 0x0a {
		return keyEvent{kind: keyAltEnter}, nil
	}
	if nextByte == 'O' {
		return reader.readSS3Sequence()
	}
	if nextByte != '[' {
		return keyEvent{kind: keyIgnore}, nil
	}
	return reader.readCSISequence()
}

func (reader *keyReader) readSS3Sequence() (keyEvent, error) {
	code, errorValue := reader.reader.ReadByte()
	if errorValue != nil {
		return keyEvent{kind: keyIgnore}, nil
	}
	switch code {
	case 'H':
		return keyEvent{kind: keyHome}, nil
	case 'F':
		return keyEvent{kind: keyEnd}, nil
	case 'A':
		return keyEvent{kind: keyUp}, nil
	case 'B':
		return keyEvent{kind: keyDown}, nil
	case 'C':
		return keyEvent{kind: keyRight}, nil
	case 'D':
		return keyEvent{kind: keyLeft}, nil
	}
	return keyEvent{kind: keyIgnore}, nil
}

func (reader *keyReader) readCSISequence() (keyEvent, error) {
	parameters := make([]byte, 0, 16)
	for len(parameters) < 32 {
		value, errorValue := reader.reader.ReadByte()
		if errorValue != nil {
			return keyEvent{kind: keyIgnore}, nil
		}
		if value >= 0x40 && value <= 0x7e {
			return reader.interpretCSI(string(parameters), value)
		}
		parameters = append(parameters, value)
	}
	return keyEvent{kind: keyIgnore}, nil
}

func (reader *keyReader) interpretCSI(parameters string, finalByte byte) (keyEvent, error) {
	if strings.HasPrefix(parameters, "<") && (finalByte == 'M' || finalByte == 'm') {
		return interpretSGRMouse(parameters[1:], finalByte), nil
	}
	switch finalByte {
	case 'A':
		return keyEvent{kind: keyUp}, nil
	case 'B':
		return keyEvent{kind: keyDown}, nil
	case 'C':
		return keyEvent{kind: keyRight}, nil
	case 'D':
		return keyEvent{kind: keyLeft}, nil
	case 'H':
		return keyEvent{kind: keyHome}, nil
	case 'F':
		return keyEvent{kind: keyEnd}, nil
	case 'u':
		return interpretKittyKey(parameters), nil
	case '~':
		return reader.interpretTildeKey(parameters)
	}
	return keyEvent{kind: keyIgnore}, nil
}

func interpretSGRMouse(parameters string, finalByte byte) keyEvent {
	if finalByte != 'M' {
		return keyEvent{kind: keyIgnore}
	}
	parts := strings.Split(parameters, ";")
	if len(parts) < 1 {
		return keyEvent{kind: keyIgnore}
	}
	button, errorValue := strconv.Atoi(parts[0])
	if errorValue != nil {
		return keyEvent{kind: keyIgnore}
	}
	switch button {
	case 64:
		return keyEvent{kind: keyScrollUp}
	case 65:
		return keyEvent{kind: keyScrollDown}
	}
	return keyEvent{kind: keyIgnore}
}

func interpretKittyKey(parameters string) keyEvent {
	parts := strings.Split(parameters, ";")
	if len(parts) == 2 && parts[0] == "13" {
		switch parts[1] {
		case "2":
			return keyEvent{kind: keyShiftEnter}
		case "3":
			return keyEvent{kind: keyAltEnter}
		}
	}
	return keyEvent{kind: keyIgnore}
}

func (reader *keyReader) interpretTildeKey(parameters string) (keyEvent, error) {
	switch parameters {
	case "1", "7":
		return keyEvent{kind: keyHome}, nil
	case "3":
		return keyEvent{kind: keyDelete}, nil
	case "4", "8":
		return keyEvent{kind: keyEnd}, nil
	case "5":
		return keyEvent{kind: keyPageUp}, nil
	case "6":
		return keyEvent{kind: keyPageDown}, nil
	case "200":
		return reader.readBracketedPaste()
	}
	if strings.HasPrefix(parameters, "27;2;13") {
		return keyEvent{kind: keyShiftEnter}, nil
	}
	return keyEvent{kind: keyIgnore}, nil
}

func (reader *keyReader) readBracketedPaste() (keyEvent, error) {
	const pasteEndMarker = "\x1b[201~"
	var collected strings.Builder
	for {
		value, errorValue := reader.reader.ReadByte()
		if errorValue != nil {
			return keyEvent{kind: keyPaste, text: normalizePasteText(collected.String())}, nil
		}
		collected.WriteByte(value)
		text := collected.String()
		if strings.HasSuffix(text, pasteEndMarker) {
			trimmed := strings.TrimSuffix(text, pasteEndMarker)
			return keyEvent{kind: keyPaste, text: normalizePasteText(trimmed)}, nil
		}
	}
}

func normalizePasteText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}
