package loop

import (
	"bytes"
	"compress/zlib"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	pdfResolveDepth           = 32
	pdfFormDepth              = 4
	pdfPageLimit              = 200
	pdfWordGapShareOfFont     = 0.15
	pdfDefaultGlyphThousandth = 500
)

type pdfName string

type pdfKeyword string

type pdfReference int

type pdfStream struct {
	Dictionary map[string]any
	Raw        []byte
}

type pdfDocument struct {
	objects map[int]any
}

type pdfLexer struct {
	content         []byte
	position        int
	readsReferences bool
}

func readPDFText(filePath string) (string, bool) {
	content, errorValue := os.ReadFile(filePath)
	if errorValue != nil {
		return "", false
	}
	return pdfTextOf(content)
}

func pdfTextOf(content []byte) (string, bool) {
	if !bytes.HasPrefix(content, []byte("%PDF-")) {
		return "", false
	}
	document := parsePDFDocument(content)
	pages := document.pages()
	if len(pages) == 0 {
		return "", false
	}
	texts := []string{}
	collectedBytes := 0
	for _, page := range pages {
		if collectedBytes > documentTextMaximumRunes*utf8.UTFMax {
			break
		}
		text, isDecodable := document.pageText(page)
		if !isDecodable {
			return "", false
		}
		if text != "" {
			texts = append(texts, text)
			collectedBytes += len(text)
		}
	}
	return strings.Join(texts, "\n"), true
}

var pdfObjectHeader = regexp.MustCompile(`(\d+)\s+\d+\s+obj\b`)

func parsePDFDocument(content []byte) pdfDocument {
	document := pdfDocument{objects: map[int]any{}}
	for _, match := range pdfObjectHeader.FindAllSubmatchIndex(content, -1) {
		number, errorValue := strconv.Atoi(string(content[match[2]:match[3]]))
		if errorValue != nil {
			continue
		}
		lexer := &pdfLexer{content: content, position: match[1], readsReferences: true}
		if value, isParsed := lexer.objectValue(); isParsed {
			document.objects[number] = value
		}
	}
	for _, value := range document.objects {
		document.addObjectStreamMembers(value)
	}
	return document
}

func (lexer *pdfLexer) objectValue() (any, bool) {
	value, isParsed := lexer.value()
	if !isParsed {
		return nil, false
	}
	dictionary, isDictionary := value.(map[string]any)
	if !isDictionary {
		return value, true
	}
	lexer.skipSpace()
	if !bytes.HasPrefix(lexer.content[lexer.position:], []byte("stream")) {
		return dictionary, true
	}
	return pdfStream{Dictionary: dictionary, Raw: lexer.streamData(dictionary)}, true
}

func (lexer *pdfLexer) streamData(dictionary map[string]any) []byte {
	start := lexer.position + len("stream")
	if start < len(lexer.content) && lexer.content[start] == '\r' {
		start++
	}
	if start < len(lexer.content) && lexer.content[start] == '\n' {
		start++
	}
	if length, isNumber := dictionary["Length"].(float64); isNumber {
		end := start + int(length)
		if end <= len(lexer.content) && bytes.HasPrefix(bytes.TrimLeft(lexer.content[end:], "\r\n \t"), []byte("endstream")) {
			return lexer.content[start:end]
		}
	}
	end := bytes.Index(lexer.content[start:], []byte("endstream"))
	if end < 0 {
		return nil
	}
	return bytes.TrimRight(lexer.content[start:start+end], "\r\n")
}

func (document pdfDocument) addObjectStreamMembers(value any) {
	stream, isStream := value.(pdfStream)
	if !isStream || stream.Dictionary["Type"] != pdfName("ObjStm") {
		return
	}
	decoded, isDecoded := document.decodedStream(stream)
	count, hasCount := stream.Dictionary["N"].(float64)
	first, hasFirst := stream.Dictionary["First"].(float64)
	if !isDecoded || !hasCount || !hasFirst {
		return
	}
	header := &pdfLexer{content: decoded}
	for index := 0; index < int(count); index++ {
		number, isNumber := header.value()
		offset, isOffset := header.value()
		numberValue, isNumberFloat := number.(float64)
		offsetValue, isOffsetFloat := offset.(float64)
		if !isNumber || !isOffset || !isNumberFloat || !isOffsetFloat {
			return
		}
		if _, isKnown := document.objects[int(numberValue)]; isKnown {
			continue
		}
		member := &pdfLexer{content: decoded, position: int(first) + int(offsetValue), readsReferences: true}
		if memberValue, isParsed := member.value(); isParsed {
			document.objects[int(numberValue)] = memberValue
		}
	}
}

func (document pdfDocument) resolve(value any) any {
	for depth := 0; depth < pdfResolveDepth; depth++ {
		reference, isReference := value.(pdfReference)
		if !isReference {
			return value
		}
		value = document.objects[int(reference)]
	}
	return nil
}

func (document pdfDocument) dictionary(value any) map[string]any {
	switch typed := document.resolve(value).(type) {
	case map[string]any:
		return typed
	case pdfStream:
		return typed.Dictionary
	}
	return nil
}

func (document pdfDocument) decodedStream(stream pdfStream) ([]byte, bool) {
	filters := []any{}
	switch typed := document.resolve(stream.Dictionary["Filter"]).(type) {
	case pdfName:
		filters = append(filters, typed)
	case []any:
		filters = typed
	}
	data := stream.Raw
	for _, filter := range filters {
		if document.resolve(filter) != pdfName("FlateDecode") {
			return nil, false
		}
		reader, errorValue := zlib.NewReader(bytes.NewReader(data))
		if errorValue != nil {
			return nil, false
		}
		inflated, errorValue := io.ReadAll(io.LimitReader(reader, documentReadMaximumBytes))
		if errorValue != nil && len(inflated) == 0 {
			return nil, false
		}
		data = inflated
	}
	return data, true
}

type pdfPage struct {
	Contents  any
	Resources map[string]any
}

func (document pdfDocument) pages() []pdfPage {
	for _, value := range document.objects {
		catalog := document.dictionary(value)
		if catalog["Type"] != pdfName("Catalog") {
			continue
		}
		pages := []pdfPage{}
		document.collectPages(catalog["Pages"], nil, map[pdfReference]bool{}, &pages)
		return pages
	}
	return nil
}

func (document pdfDocument) collectPages(node any, inherited map[string]any, isVisited map[pdfReference]bool, pages *[]pdfPage) {
	if reference, isReference := node.(pdfReference); isReference {
		if isVisited[reference] {
			return
		}
		isVisited[reference] = true
	}
	dictionary := document.dictionary(node)
	if dictionary == nil || len(*pages) >= pdfPageLimit {
		return
	}
	resources := inherited
	if own := document.dictionary(dictionary["Resources"]); own != nil {
		resources = own
	}
	if kids, hasKids := document.resolve(dictionary["Kids"]).([]any); hasKids {
		for _, kid := range kids {
			document.collectPages(kid, resources, isVisited, pages)
		}
		return
	}
	*pages = append(*pages, pdfPage{Contents: dictionary["Contents"], Resources: resources})
}

func (document pdfDocument) contentBytes(contents any) []byte {
	streams := []any{contents}
	if array, isArray := document.resolve(contents).([]any); isArray {
		streams = array
	}
	joined := []byte{}
	for _, item := range streams {
		stream, isStream := document.resolve(item).(pdfStream)
		if !isStream {
			continue
		}
		if decoded, isDecoded := document.decodedStream(stream); isDecoded {
			joined = append(append(joined, decoded...), '\n')
		}
	}
	return joined
}

func (document pdfDocument) pageText(page pdfPage) (string, bool) {
	writer := &pdfTextWriter{document: document, fonts: map[pdfReference]pdfFont{}}
	writer.run(document.contentBytes(page.Contents), page.Resources, identityMatrix(), 0)
	return collapseBlankRuns(writer.builder.String()), !writer.hasUndecodableText
}

type pdfMatrix [6]float64

func identityMatrix() pdfMatrix {
	return pdfMatrix{1, 0, 0, 1, 0, 0}
}

func (left pdfMatrix) times(right pdfMatrix) pdfMatrix {
	return pdfMatrix{
		left[0]*right[0] + left[1]*right[2],
		left[0]*right[1] + left[1]*right[3],
		left[2]*right[0] + left[3]*right[2],
		left[2]*right[1] + left[3]*right[3],
		left[4]*right[0] + left[5]*right[2] + right[4],
		left[4]*right[1] + left[5]*right[3] + right[5],
	}
}

func translation(x float64, y float64) pdfMatrix {
	return pdfMatrix{1, 0, 0, 1, x, y}
}

type pdfTextState struct {
	Font             pdfFont
	FontSize         float64
	Leading          float64
	CharacterSpacing float64
	WordSpacing      float64
	HorizontalScale  float64
	Matrix           pdfMatrix
	Line             pdfMatrix
}

type pdfMarkedContent struct {
	ActualText string
	IsWritten  bool
}

type pdfTextWriter struct {
	document           pdfDocument
	fonts              map[pdfReference]pdfFont
	builder            strings.Builder
	lastX              float64
	lastY              float64
	hasWritten         bool
	hasUndecodableText bool
	marked             []pdfMarkedContent
}

func (writer *pdfTextWriter) run(content []byte, resources map[string]any, transform pdfMatrix, depth int) {
	lexer := &pdfLexer{content: content}
	operands := []any{}
	transforms := []pdfMatrix{}
	text := pdfTextState{Matrix: identityMatrix(), Line: identityMatrix(), HorizontalScale: 1}
	for {
		token, isParsed := lexer.value()
		if !isParsed {
			return
		}
		operator, isOperator := token.(pdfKeyword)
		if !isOperator {
			operands = append(operands, token)
			continue
		}
		switch operator {
		case "q":
			transforms = append(transforms, transform)
		case "Q":
			if len(transforms) > 0 {
				transform, transforms = transforms[len(transforms)-1], transforms[:len(transforms)-1]
			}
		case "cm":
			if matrix, isMatrix := matrixOperand(operands); isMatrix {
				transform = matrix.times(transform)
			}
		case "BT":
			text.Matrix, text.Line = identityMatrix(), identityMatrix()
		case "Tc":
			text.CharacterSpacing = numberOperand(operands, 0)
		case "Tw":
			text.WordSpacing = numberOperand(operands, 0)
		case "Tz":
			text.HorizontalScale = numberOperand(operands, 0) / 100
		case "BMC", "BDC":
			writer.marked = append(writer.marked, pdfMarkedContent{ActualText: actualTextOf(writer.document, lastOperand(operands))})
		case "EMC":
			if len(writer.marked) > 0 {
				writer.marked = writer.marked[:len(writer.marked)-1]
			}
		case "Tf":
			writer.setFont(&text, operands, resources)
		case "TL":
			text.Leading = numberOperand(operands, 0)
		case "Tm":
			if matrix, isMatrix := matrixOperand(operands); isMatrix {
				text.Matrix, text.Line = matrix, matrix
			}
		case "Td", "TD":
			writer.moveLine(&text, numberOperand(operands, 0), numberOperand(operands, 1))
			if operator == "TD" {
				text.Leading = -numberOperand(operands, 1)
			}
		case "T*":
			writer.moveLine(&text, 0, -text.Leading)
		case "Tj":
			writer.show(&text, transform, lastOperand(operands))
		case "'", "\"":
			writer.moveLine(&text, 0, -text.Leading)
			writer.show(&text, transform, lastOperand(operands))
		case "TJ":
			writer.showArray(&text, transform, lastOperand(operands))
		case "Do":
			writer.drawForm(lastOperand(operands), resources, transform, depth)
		case "BI":
			lexer.skipInlineImage()
		}
		operands = operands[:0]
	}
}

func (writer *pdfTextWriter) moveLine(text *pdfTextState, x float64, y float64) {
	text.Line = translation(x, y).times(text.Line)
	text.Matrix = text.Line
}

func (writer *pdfTextWriter) setFont(text *pdfTextState, operands []any, resources map[string]any) {
	text.FontSize = numberOperand(operands, 1)
	name, isName := firstOperand(operands).(pdfName)
	if !isName {
		return
	}
	fontValue := writer.document.dictionary(resources["Font"])[string(name)]
	reference, isReference := fontValue.(pdfReference)
	if !isReference {
		text.Font = writer.document.font(fontValue)
		return
	}
	if font, isKnown := writer.fonts[reference]; isKnown {
		text.Font = font
		return
	}
	font := writer.document.font(reference)
	writer.fonts[reference] = font
	text.Font = font
}

func (writer *pdfTextWriter) show(text *pdfTextState, transform pdfMatrix, operand any) {
	encoded, isString := operand.([]byte)
	if !isString {
		return
	}
	writer.place(*text, transform)
	decoded, isDecodable := text.Font.decode(encoded)
	if !isDecodable && !writer.isInsideActualText() {
		writer.hasUndecodableText = true
	}
	writer.builder.WriteString(writer.visibleText(decoded))
	text.Matrix = translation(text.advance(encoded), 0).times(text.Matrix)
	writer.lastX = text.Matrix.times(transform)[4]
}

func (writer *pdfTextWriter) isInsideActualText() bool {
	for _, marked := range writer.marked {
		if marked.ActualText != "" {
			return true
		}
	}
	return false
}

func (writer *pdfTextWriter) visibleText(decoded string) string {
	for index := len(writer.marked) - 1; index >= 0; index-- {
		if writer.marked[index].ActualText == "" {
			continue
		}
		if writer.marked[index].IsWritten {
			return ""
		}
		writer.marked[index].IsWritten = true
		return writer.marked[index].ActualText
	}
	return decoded
}

func (text pdfTextState) advance(encoded []byte) float64 {
	total := 0.0
	for index := 0; index+text.Font.CodeLength <= len(encoded); index += text.Font.CodeLength {
		code := codeOf(encoded[index : index+text.Font.CodeLength])
		total += text.Font.width(code)/1000*text.FontSize + text.CharacterSpacing
		if text.Font.CodeLength == 1 && code == ' ' {
			total += text.WordSpacing
		}
	}
	return total * text.HorizontalScale
}

func (writer *pdfTextWriter) showArray(text *pdfTextState, transform pdfMatrix, operand any) {
	items, isArray := operand.([]any)
	if !isArray {
		return
	}
	for _, item := range items {
		switch typed := item.(type) {
		case []byte:
			writer.show(text, transform, typed)
		case float64:
			text.Matrix = translation(-typed/1000*text.FontSize*text.HorizontalScale, 0).times(text.Matrix)
		}
	}
}

func (writer *pdfTextWriter) place(text pdfTextState, transform pdfMatrix) {
	origin := text.Matrix.times(transform)
	fontHeight := math.Hypot(origin[2], origin[3]) * text.FontSize
	x, y := origin[4], origin[5]
	switch {
	case !writer.hasWritten:
	case math.Abs(y-writer.lastY) > math.Max(fontHeight/2, 1):
		writer.writeSeparator("\n")
	case math.Abs(x-writer.lastX) > math.Max(fontHeight*pdfWordGapShareOfFont, 0.5):
		writer.writeSeparator(" ")
	}
	writer.lastY = y
	writer.hasWritten = true
}

func actualTextOf(document pdfDocument, operand any) string {
	properties := document.dictionary(operand)
	encoded, isString := document.resolve(properties["ActualText"]).([]byte)
	if !isString {
		return ""
	}
	if bytes.HasPrefix(encoded, []byte{0xFE, 0xFF}) {
		return utf16Text(encoded[2:])
	}
	return latinText(encoded)
}

func (writer *pdfTextWriter) writeSeparator(separator string) {
	written := writer.builder.String()
	if strings.HasSuffix(written, separator) || strings.HasSuffix(written, "\n") {
		return
	}
	writer.builder.WriteString(separator)
}

func (writer *pdfTextWriter) drawForm(operand any, resources map[string]any, transform pdfMatrix, depth int) {
	name, isName := operand.(pdfName)
	if !isName || depth >= pdfFormDepth {
		return
	}
	form, isStream := writer.document.resolve(writer.document.dictionary(resources["XObject"])[string(name)]).(pdfStream)
	if !isStream || form.Dictionary["Subtype"] != pdfName("Form") {
		return
	}
	content, isDecoded := writer.document.decodedStream(form)
	if !isDecoded {
		return
	}
	formResources := resources
	if own := writer.document.dictionary(form.Dictionary["Resources"]); own != nil {
		formResources = own
	}
	formTransform := transform
	if matrix, isMatrix := matrixOperand(asOperands(writer.document.resolve(form.Dictionary["Matrix"]))); isMatrix {
		formTransform = matrix.times(transform)
	}
	writer.run(content, formResources, formTransform, depth+1)
}

func asOperands(value any) []any {
	array, _ := value.([]any)
	return array
}

func matrixOperand(operands []any) (pdfMatrix, bool) {
	if len(operands) < 6 {
		return pdfMatrix{}, false
	}
	matrix := pdfMatrix{}
	for index := range matrix {
		number, isNumber := operands[len(operands)-6+index].(float64)
		if !isNumber {
			return pdfMatrix{}, false
		}
		matrix[index] = number
	}
	return matrix, true
}

func numberOperand(operands []any, index int) float64 {
	if index >= len(operands) {
		return 0
	}
	number, _ := operands[index].(float64)
	return number
}

func firstOperand(operands []any) any {
	if len(operands) == 0 {
		return nil
	}
	return operands[0]
}

func lastOperand(operands []any) any {
	if len(operands) == 0 {
		return nil
	}
	return operands[len(operands)-1]
}

type pdfFont struct {
	CodeLength   int
	Unicode      map[uint32]string
	IsSimple     bool
	Widths       map[uint32]float64
	DefaultWidth float64
}

func (font pdfFont) width(code uint32) float64 {
	if width, isKnown := font.Widths[code]; isKnown {
		return width
	}
	return font.DefaultWidth
}

func (document pdfDocument) font(reference any) pdfFont {
	dictionary := document.dictionary(reference)
	font := pdfFont{CodeLength: 1, IsSimple: dictionary["Subtype"] != pdfName("Type0")}
	if font.IsSimple {
		font.Widths, font.DefaultWidth = document.simpleFontWidths(dictionary)
	} else {
		font.CodeLength = 2
		font.Widths, font.DefaultWidth = document.compositeFontWidths(dictionary)
	}
	stream, hasUnicode := document.resolve(dictionary["ToUnicode"]).(pdfStream)
	if !hasUnicode {
		return font
	}
	cmap, isDecoded := document.decodedStream(stream)
	if !isDecoded {
		return font
	}
	font.Unicode, font.CodeLength = parseUnicodeMap(cmap, font.CodeLength)
	return font
}

func (document pdfDocument) simpleFontWidths(dictionary map[string]any) (map[uint32]float64, float64) {
	widths := map[uint32]float64{}
	firstCode, _ := document.resolve(dictionary["FirstChar"]).(float64)
	listed, _ := document.resolve(dictionary["Widths"]).([]any)
	for offset, item := range listed {
		if width, isNumber := document.resolve(item).(float64); isNumber {
			widths[uint32(firstCode)+uint32(offset)] = width
		}
	}
	missing, isNumber := document.resolve(document.dictionary(dictionary["FontDescriptor"])["MissingWidth"]).(float64)
	if !isNumber || missing == 0 {
		missing = pdfDefaultGlyphThousandth
	}
	return widths, missing
}

func (document pdfDocument) compositeFontWidths(dictionary map[string]any) (map[uint32]float64, float64) {
	descendants, _ := document.resolve(dictionary["DescendantFonts"]).([]any)
	if len(descendants) == 0 {
		return nil, 1000
	}
	descendant := document.dictionary(descendants[0])
	defaultWidth, isNumber := document.resolve(descendant["DW"]).(float64)
	if !isNumber {
		defaultWidth = 1000
	}
	return document.compositeWidthList(document.resolve(descendant["W"])), defaultWidth
}

func (document pdfDocument) compositeWidthList(value any) map[uint32]float64 {
	widths := map[uint32]float64{}
	items, _ := value.([]any)
	for index := 0; index+1 < len(items); {
		first, isFirst := document.resolve(items[index]).(float64)
		if !isFirst {
			return widths
		}
		if listed, isList := document.resolve(items[index+1]).([]any); isList {
			for offset, item := range listed {
				if width, isNumber := document.resolve(item).(float64); isNumber {
					widths[uint32(first)+uint32(offset)] = width
				}
			}
			index += 2
			continue
		}
		last, isLast := document.resolve(items[index+1]).(float64)
		if index+2 >= len(items) || !isLast || last < first || last-first > 0xFFFF {
			return widths
		}
		width, _ := document.resolve(items[index+2]).(float64)
		for code := uint32(first); code <= uint32(last); code++ {
			widths[code] = width
		}
		index += 3
	}
	return widths
}

func (font pdfFont) decode(encoded []byte) (string, bool) {
	if font.Unicode == nil {
		return latinText(encoded), font.IsSimple
	}
	builder := strings.Builder{}
	for index := 0; index+font.CodeLength <= len(encoded); index += font.CodeLength {
		builder.WriteString(font.Unicode[codeOf(encoded[index:index+font.CodeLength])])
	}
	return builder.String(), true
}

func latinText(encoded []byte) string {
	runes := make([]rune, 0, len(encoded))
	for _, character := range encoded {
		runes = append(runes, rune(character))
	}
	return string(runes)
}

func codeOf(encoded []byte) uint32 {
	code := uint32(0)
	for _, character := range encoded {
		code = code<<8 | uint32(character)
	}
	return code
}

func parseUnicodeMap(content []byte, defaultCodeLength int) (map[uint32]string, int) {
	unicode := map[uint32]string{}
	codeLength := defaultCodeLength
	lexer := &pdfLexer{content: content}
	operands := []any{}
	for {
		token, isParsed := lexer.value()
		if !isParsed {
			return unicode, codeLength
		}
		keyword, isKeyword := token.(pdfKeyword)
		if !isKeyword {
			operands = append(operands, token)
			continue
		}
		switch keyword {
		case "endcodespacerange":
			if low, isBytes := firstOperand(operands).([]byte); isBytes && len(low) > 0 {
				codeLength = len(low)
			}
		case "endbfchar":
			addCharacterMappings(unicode, operands)
		case "endbfrange":
			addRangeMappings(unicode, operands)
		}
		operands = operands[:0]
	}
}

func addCharacterMappings(unicode map[uint32]string, operands []any) {
	for index := 0; index+1 < len(operands); index += 2 {
		source, isSource := operands[index].([]byte)
		target, isTarget := operands[index+1].([]byte)
		if isSource && isTarget {
			unicode[codeOf(source)] = utf16Text(target)
		}
	}
}

func addRangeMappings(unicode map[uint32]string, operands []any) {
	for index := 0; index+2 < len(operands); index += 3 {
		low, isLow := operands[index].([]byte)
		high, isHigh := operands[index+1].([]byte)
		if !isLow || !isHigh || codeOf(high) < codeOf(low) || codeOf(high)-codeOf(low) > 0xFFFF {
			continue
		}
		switch target := operands[index+2].(type) {
		case []byte:
			addIncrementingRange(unicode, codeOf(low), codeOf(high), target)
		case []any:
			for offset, item := range target {
				if mapped, isBytes := item.([]byte); isBytes && codeOf(low)+uint32(offset) <= codeOf(high) {
					unicode[codeOf(low)+uint32(offset)] = utf16Text(mapped)
				}
			}
		}
	}
}

func addIncrementingRange(unicode map[uint32]string, low uint32, high uint32, target []byte) {
	units := utf16Units(target)
	if len(units) == 0 {
		return
	}
	for code := low; code <= high; code++ {
		shifted := append([]uint16{}, units...)
		shifted[len(shifted)-1] += uint16(code - low)
		unicode[code] = string(utf16.Decode(shifted))
	}
}

func utf16Units(encoded []byte) []uint16 {
	units := make([]uint16, 0, len(encoded)/2)
	for index := 0; index+1 < len(encoded); index += 2 {
		units = append(units, uint16(encoded[index])<<8|uint16(encoded[index+1]))
	}
	return units
}

func utf16Text(encoded []byte) string {
	return string(utf16.Decode(utf16Units(encoded)))
}

func isPDFWhitespace(character byte) bool {
	return character == ' ' || character == '\n' || character == '\r' || character == '\t' || character == '\f' || character == 0
}

func isPDFDelimiter(character byte) bool {
	return strings.IndexByte("()<>[]{}/%", character) >= 0
}

func (lexer *pdfLexer) skipSpace() {
	for lexer.position < len(lexer.content) {
		character := lexer.content[lexer.position]
		if character == '%' {
			for lexer.position < len(lexer.content) && lexer.content[lexer.position] != '\n' && lexer.content[lexer.position] != '\r' {
				lexer.position++
			}
			continue
		}
		if !isPDFWhitespace(character) {
			return
		}
		lexer.position++
	}
}

func (lexer *pdfLexer) value() (any, bool) {
	lexer.skipSpace()
	if lexer.position >= len(lexer.content) {
		return nil, false
	}
	switch character := lexer.content[lexer.position]; {
	case character == '/':
		return lexer.name(), true
	case character == '(':
		return lexer.literalString(), true
	case character == '<' && lexer.peek(1) == '<':
		return lexer.dictionaryValue()
	case character == '<':
		return lexer.hexString(), true
	case character == '[':
		return lexer.arrayValue()
	case character == ']' || character == '>' || character == ')' || character == '{' || character == '}':
		lexer.position++
		return pdfKeyword(string(character)), true
	default:
		return lexer.numberOrKeyword(), true
	}
}

func (lexer *pdfLexer) peek(offset int) byte {
	if lexer.position+offset >= len(lexer.content) {
		return 0
	}
	return lexer.content[lexer.position+offset]
}

func (lexer *pdfLexer) regularRun() string {
	start := lexer.position
	for lexer.position < len(lexer.content) && !isPDFWhitespace(lexer.content[lexer.position]) && !isPDFDelimiter(lexer.content[lexer.position]) {
		lexer.position++
	}
	if lexer.position == start {
		lexer.position++
	}
	return string(lexer.content[start:lexer.position])
}

func (lexer *pdfLexer) name() pdfName {
	lexer.position++
	start := lexer.position
	for lexer.position < len(lexer.content) && !isPDFWhitespace(lexer.content[lexer.position]) && !isPDFDelimiter(lexer.content[lexer.position]) {
		lexer.position++
	}
	return pdfName(decodeNameEscapes(string(lexer.content[start:lexer.position])))
}

func decodeNameEscapes(raw string) string {
	if !strings.Contains(raw, "#") {
		return raw
	}
	builder := strings.Builder{}
	for index := 0; index < len(raw); index++ {
		if raw[index] == '#' && index+2 < len(raw) {
			if value, errorValue := strconv.ParseUint(raw[index+1:index+3], 16, 8); errorValue == nil {
				builder.WriteByte(byte(value))
				index += 2
				continue
			}
		}
		builder.WriteByte(raw[index])
	}
	return builder.String()
}

func (lexer *pdfLexer) numberOrKeyword() any {
	word := lexer.regularRun()
	number, errorValue := strconv.ParseFloat(word, 64)
	if errorValue != nil {
		return pdfKeyword(word)
	}
	if lexer.readsReferences {
		if reference, isReference := lexer.referenceAfter(word); isReference {
			return reference
		}
	}
	return number
}

var pdfReferenceTail = regexp.MustCompile(`^\s+\d+\s+R\b`)

func (lexer *pdfLexer) referenceAfter(word string) (pdfReference, bool) {
	number, errorValue := strconv.Atoi(word)
	if errorValue != nil {
		return 0, false
	}
	tail := pdfReferenceTail.Find(lexer.content[lexer.position:min(len(lexer.content), lexer.position+32)])
	if tail == nil {
		return 0, false
	}
	lexer.position += len(tail)
	return pdfReference(number), true
}

func (lexer *pdfLexer) dictionaryValue() (any, bool) {
	lexer.position += 2
	dictionary := map[string]any{}
	for {
		lexer.skipSpace()
		if lexer.position+1 >= len(lexer.content) {
			return nil, false
		}
		if lexer.content[lexer.position] == '>' && lexer.content[lexer.position+1] == '>' {
			lexer.position += 2
			return dictionary, true
		}
		key, isParsed := lexer.value()
		name, isName := key.(pdfName)
		if !isParsed || !isName {
			return nil, false
		}
		value, isParsed := lexer.value()
		if !isParsed {
			return nil, false
		}
		dictionary[string(name)] = value
	}
}

func (lexer *pdfLexer) arrayValue() (any, bool) {
	lexer.position++
	array := []any{}
	for {
		lexer.skipSpace()
		if lexer.position >= len(lexer.content) {
			return nil, false
		}
		if lexer.content[lexer.position] == ']' {
			lexer.position++
			return array, true
		}
		item, isParsed := lexer.value()
		if !isParsed {
			return nil, false
		}
		array = append(array, item)
	}
}

func (lexer *pdfLexer) hexString() []byte {
	lexer.position++
	digits := []byte{}
	for lexer.position < len(lexer.content) && lexer.content[lexer.position] != '>' {
		if character := lexer.content[lexer.position]; !isPDFWhitespace(character) {
			digits = append(digits, character)
		}
		lexer.position++
	}
	lexer.position++
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	decoded := make([]byte, 0, len(digits)/2)
	for index := 0; index+1 < len(digits); index += 2 {
		value, errorValue := strconv.ParseUint(string(digits[index:index+2]), 16, 8)
		if errorValue != nil {
			return decoded
		}
		decoded = append(decoded, byte(value))
	}
	return decoded
}

func (lexer *pdfLexer) literalString() []byte {
	lexer.position++
	decoded := []byte{}
	depth := 1
	for lexer.position < len(lexer.content) {
		character := lexer.content[lexer.position]
		lexer.position++
		switch character {
		case '\\':
			decoded = append(decoded, lexer.escapedCharacter()...)
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return decoded
			}
		}
		decoded = append(decoded, character)
	}
	return decoded
}

var pdfEscapes = map[byte]byte{'n': '\n', 'r': '\r', 't': '\t', 'b': '\b', 'f': '\f', '(': '(', ')': ')', '\\': '\\'}

func (lexer *pdfLexer) escapedCharacter() []byte {
	if lexer.position >= len(lexer.content) {
		return nil
	}
	character := lexer.content[lexer.position]
	lexer.position++
	if escaped, isKnown := pdfEscapes[character]; isKnown {
		return []byte{escaped}
	}
	if character == '\r' || character == '\n' {
		if character == '\r' && lexer.peek(0) == '\n' {
			lexer.position++
		}
		return nil
	}
	if character < '0' || character > '7' {
		return []byte{character}
	}
	value := int(character - '0')
	for count := 0; count < 2 && lexer.peek(0) >= '0' && lexer.peek(0) <= '7'; count++ {
		value = value*8 + int(lexer.content[lexer.position]-'0')
		lexer.position++
	}
	return []byte{byte(value)}
}

func (lexer *pdfLexer) skipInlineImage() {
	searchFrom := lexer.position
	for {
		found := bytes.Index(lexer.content[searchFrom:], []byte("EI"))
		if found < 0 {
			lexer.position = len(lexer.content)
			return
		}
		start := searchFrom + found
		after := start + 2
		if start > 0 && isPDFWhitespace(lexer.content[start-1]) && (after >= len(lexer.content) || isPDFWhitespace(lexer.content[after])) {
			lexer.position = after
			return
		}
		searchFrom = after
	}
}
