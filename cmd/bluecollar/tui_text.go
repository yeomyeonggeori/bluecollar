package main

import (
	"strings"
	"unicode"
)

func runeDisplayWidth(character rune) int {
	if character == 0 || unicode.Is(unicode.Mn, character) || unicode.Is(unicode.Me, character) || unicode.Is(unicode.Cf, character) {
		return 0
	}
	if isWideRune(character) {
		return 2
	}
	return 1
}

func isWideRune(character rune) bool {
	switch {
	case character >= 0x1100 && character <= 0x115f:
		return true
	case character >= 0x2e80 && character <= 0x303e:
		return true
	case character >= 0x3041 && character <= 0x33ff:
		return true
	case character >= 0x3400 && character <= 0x4dbf:
		return true
	case character >= 0x4e00 && character <= 0xa4cf:
		return true
	case character >= 0xac00 && character <= 0xd7a3:
		return true
	case character >= 0xf900 && character <= 0xfaff:
		return true
	case character >= 0xfe10 && character <= 0xfe6f:
		return true
	case character >= 0xff01 && character <= 0xff60:
		return true
	case character >= 0xffe0 && character <= 0xffe6:
		return true
	case character >= 0x1f300 && character <= 0x1faff:
		return true
	case character >= 0x20000 && character <= 0x3ffff:
		return true
	}
	return false
}

func displayWidth(text string) int {
	width := 0
	inEscape := false
	for _, character := range text {
		if inEscape {
			if character == 'm' {
				inEscape = false
			}
			continue
		}
		if character == '\x1b' {
			inEscape = true
			continue
		}
		width += runeDisplayWidth(character)
	}
	return width
}

func truncateToWidth(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if displayWidth(text) <= maxWidth {
		return text
	}
	if maxWidth == 1 {
		return "…"
	}
	target := maxWidth - 1
	var builder strings.Builder
	width := 0
	inEscape := false
	hasEscape := false
	for _, character := range text {
		if inEscape {
			builder.WriteRune(character)
			if character == 'm' {
				inEscape = false
			}
			continue
		}
		if character == '\x1b' {
			inEscape = true
			hasEscape = true
			builder.WriteRune(character)
			continue
		}
		characterWidth := runeDisplayWidth(character)
		if width+characterWidth > target {
			break
		}
		builder.WriteRune(character)
		width += characterWidth
	}
	builder.WriteRune('…')
	if hasEscape {
		builder.WriteString(styleReset)
	}
	return builder.String()
}

func padRightToWidth(text string, width int) string {
	current := displayWidth(text)
	if current >= width {
		return truncateToWidth(text, width)
	}
	return text + strings.Repeat(" ", width-current)
}

func alignColumns(left string, right string, width int) string {
	if width <= 0 {
		return ""
	}
	rightWidth := displayWidth(right)
	if rightWidth >= width {
		return truncateToWidth(right, width)
	}
	availableLeft := width - rightWidth - 1
	if availableLeft < 0 {
		availableLeft = 0
	}
	trimmedLeft := truncateToWidth(left, availableLeft)
	gap := width - displayWidth(trimmedLeft) - rightWidth
	if gap < 1 {
		gap = 1
	}
	return trimmedLeft + strings.Repeat(" ", gap) + right
}

func wrapPlainText(text string, width int) []string {
	if width <= 1 {
		return []string{text}
	}
	paragraphs := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapSingleLine(paragraph, width)...)
	}
	return lines
}

func wrapSingleLine(line string, width int) []string {
	result := []string{}
	var current strings.Builder
	currentWidth := 0
	lastSpaceByte := -1
	lastSpaceWidth := 0
	for _, character := range line {
		characterWidth := runeDisplayWidth(character)
		if currentWidth+characterWidth > width && currentWidth > 0 {
			if lastSpaceByte >= 0 && currentWidth-lastSpaceWidth < width/2 {
				built := current.String()
				result = append(result, strings.TrimRight(built[:lastSpaceByte], " "))
				remainder := strings.TrimLeft(built[lastSpaceByte+1:], " ")
				current.Reset()
				current.WriteString(remainder)
				currentWidth = displayWidth(remainder)
				lastSpaceByte = strings.LastIndexByte(remainder, ' ')
				if lastSpaceByte >= 0 {
					lastSpaceWidth = displayWidth(remainder[:lastSpaceByte])
				} else {
					lastSpaceWidth = 0
				}
			} else {
				result = append(result, strings.TrimRight(current.String(), " "))
				current.Reset()
				currentWidth = 0
				lastSpaceByte = -1
				lastSpaceWidth = 0
				if character == ' ' {
					continue
				}
			}
		}
		if character == ' ' {
			lastSpaceByte = current.Len()
			lastSpaceWidth = currentWidth
		}
		current.WriteRune(character)
		currentWidth += characterWidth
	}
	if current.Len() > 0 || len(result) == 0 {
		result = append(result, current.String())
	}
	return result
}

func renderMarkdownLines(text string, width int) []string {
	if width < 12 {
		width = 12
	}
	rawLines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	rendered := make([]string, 0, len(rawLines))
	inCodeFence := false
	for _, rawLine := range rawLines {
		trimmed := strings.TrimSpace(rawLine)
		if strings.HasPrefix(trimmed, "```") {
			if !inCodeFence {
				inCodeFence = true
				language := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				header := inkFaint + "╭─ " + language + styleReset
				if language == "" {
					header = inkFaint + "╭───" + styleReset
				}
				rendered = append(rendered, header)
			} else {
				inCodeFence = false
				rendered = append(rendered, inkFaint+"╰───"+styleReset)
			}
			continue
		}
		if inCodeFence {
			for _, wrapped := range wrapSingleLine(rawLine, max(width-2, 8)) {
				rendered = append(rendered, inkFaint+"│ "+styleReset+"\x1b[38;5;152m"+wrapped+styleReset)
			}
			continue
		}
		if trimmed == "" {
			rendered = append(rendered, "")
			continue
		}
		if heading, isHeading := parseMarkdownHeading(trimmed); isHeading {
			for _, wrapped := range wrapSingleLine(heading, width) {
				rendered = append(rendered, styleBold+inkTask+wrapped+styleReset)
			}
			continue
		}
		if bulletPrefix, body, isBullet := parseMarkdownListLine(rawLine); isBullet {
			prefixWidth := displayWidth(bulletPrefix)
			indent := strings.Repeat(" ", prefixWidth)
			for index, wrapped := range wrapSingleLine(body, max(width-prefixWidth, 8)) {
				if index == 0 {
					rendered = append(rendered, inkFaint+bulletPrefix+styleReset+styleInlineMarkdown(wrapped))
				} else {
					rendered = append(rendered, indent+styleInlineMarkdown(wrapped))
				}
			}
			continue
		}
		for _, wrapped := range wrapSingleLine(rawLine, width) {
			rendered = append(rendered, styleInlineMarkdown(wrapped))
		}
	}
	return rendered
}

func parseMarkdownHeading(trimmed string) (string, bool) {
	hashes := 0
	for hashes < len(trimmed) && trimmed[hashes] == '#' {
		hashes++
	}
	if hashes == 0 || hashes > 4 || hashes >= len(trimmed) || trimmed[hashes] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[hashes+1:]), true
}

func parseMarkdownListLine(line string) (string, string, bool) {
	leadingSpaces := len(line) - len(strings.TrimLeft(line, " "))
	trimmed := line[leadingSpaces:]
	indent := strings.Repeat(" ", min(leadingSpaces, 6))
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return indent + "• ", trimmed[2:], true
	}
	dotIndex := strings.Index(trimmed, ". ")
	if dotIndex > 0 && dotIndex <= 3 {
		isDigits := true
		for _, character := range trimmed[:dotIndex] {
			if character < '0' || character > '9' {
				isDigits = false
				break
			}
		}
		if isDigits {
			return indent + trimmed[:dotIndex+2], trimmed[dotIndex+2:], true
		}
	}
	return "", "", false
}

func styleInlineMarkdown(line string) string {
	var builder strings.Builder
	remaining := line
	for len(remaining) > 0 {
		codeIndex := strings.IndexByte(remaining, '`')
		boldIndex := strings.Index(remaining, "**")
		nextIndex := -1
		isBold := false
		if codeIndex >= 0 && (boldIndex < 0 || codeIndex < boldIndex) {
			nextIndex = codeIndex
		} else if boldIndex >= 0 {
			nextIndex = boldIndex
			isBold = true
		}
		if nextIndex < 0 {
			builder.WriteString(remaining)
			break
		}
		builder.WriteString(remaining[:nextIndex])
		if isBold {
			afterOpen := remaining[nextIndex+2:]
			closeIndex := strings.Index(afterOpen, "**")
			if closeIndex < 0 {
				builder.WriteString("**")
				remaining = afterOpen
				continue
			}
			builder.WriteString(styleBold)
			builder.WriteString(afterOpen[:closeIndex])
			builder.WriteString(styleReset)
			remaining = afterOpen[closeIndex+2:]
			continue
		}
		afterOpen := remaining[nextIndex+1:]
		closeIndex := strings.IndexByte(afterOpen, '`')
		if closeIndex < 0 {
			builder.WriteByte('`')
			remaining = afterOpen
			continue
		}
		builder.WriteString("\x1b[38;5;152m")
		builder.WriteString(afterOpen[:closeIndex])
		builder.WriteString(styleReset)
		remaining = afterOpen[closeIndex+1:]
	}
	return builder.String()
}
