package loop

import (
	"archive/zip"
	"encoding/xml"
	"html"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	documentTextMaximumRunes = 6000
	documentExtractLimit     = 3
	documentReadMaximumBytes = 32 << 20
)

type documentExtract struct {
	Path      string `json:"path"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

type documentReader func(filePath string) (string, bool)

var documentReaders = map[string]documentReader{
	".docx": readWordDocumentText,
	".xlsx": readWorkbookText,
	".pptx": readPresentationText,
	".pdf":  readPDFText,
	".md":   readPlainText,
	".txt":  readPlainText,
	".csv":  readPlainText,
	".tsv":  readPlainText,
	".json": readPlainText,
	".html": readMarkupText,
	".htm":  readMarkupText,
}

func documentExtractOf(workspaceRootPath string, recordedPath string) (documentExtract, bool) {
	reader, isReadable := documentReaders[strings.ToLower(filepath.Ext(strings.TrimSpace(recordedPath)))]
	if !isReadable {
		return documentExtract{}, false
	}
	filePath, isCheckable := checkableAttachmentPath(workspaceRootPath, toolcontract.FileAttachment{DevicePath: recordedPath})
	if !isCheckable || !isReadableRegularFile(filePath) {
		return documentExtract{}, false
	}
	text, isRead := reader(filePath)
	text = strings.TrimSpace(text)
	if !isRead || text == "" {
		return documentExtract{}, false
	}
	bounded, isTruncated := boundedRunes(text, documentTextMaximumRunes)
	return documentExtract{Path: strings.TrimSpace(recordedPath), Text: bounded, Truncated: isTruncated}, true
}

func isReadableRegularFile(filePath string) bool {
	fileInformation, errorValue := os.Stat(filePath)
	return errorValue == nil && fileInformation.Mode().IsRegular() && fileInformation.Size() > 0 && fileInformation.Size() <= documentReadMaximumBytes
}

func boundedRunes(text string, maximumRunes int) (string, bool) {
	if utf8.RuneCountInString(text) <= maximumRunes {
		return text, false
	}
	return string([]rune(text)[:maximumRunes]), true
}

func readPlainText(filePath string) (string, bool) {
	content, errorValue := os.ReadFile(filePath)
	if errorValue != nil || !utf8.Valid(content) {
		return "", false
	}
	return string(content), true
}

var markupHiddenBlocks = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
var markupLineBreaks = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/li|/h[1-6])[^>]*>`)
var markupTags = regexp.MustCompile(`(?s)<[^>]*>`)

func readMarkupText(filePath string) (string, bool) {
	content, isRead := readPlainText(filePath)
	if !isRead {
		return "", false
	}
	content = markupHiddenBlocks.ReplaceAllString(content, "")
	content = markupLineBreaks.ReplaceAllString(content, "\n")
	content = markupTags.ReplaceAllString(content, " ")
	return collapseBlankRuns(html.UnescapeString(content)), true
}

func collapseBlankRuns(text string) string {
	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		if compacted := strings.Join(strings.Fields(line), " "); compacted != "" {
			lines = append(lines, compacted)
		}
	}
	return strings.Join(lines, "\n")
}

func readZipEntries(filePath string, keep func(name string) bool) (map[string][]byte, bool) {
	archive, errorValue := zip.OpenReader(filePath)
	if errorValue != nil {
		return nil, false
	}
	defer archive.Close()
	entries := map[string][]byte{}
	for _, file := range archive.File {
		if !keep(file.Name) {
			continue
		}
		content, isRead := readZipEntry(file)
		if !isRead {
			return nil, false
		}
		entries[file.Name] = content
	}
	return entries, true
}

func readZipEntry(file *zip.File) ([]byte, bool) {
	if file.UncompressedSize64 > documentReadMaximumBytes {
		return nil, false
	}
	reader, errorValue := file.Open()
	if errorValue != nil {
		return nil, false
	}
	defer reader.Close()
	content, errorValue := io.ReadAll(io.LimitReader(reader, documentReadMaximumBytes))
	return content, errorValue == nil
}

var wordPartPattern = regexp.MustCompile(`^word/(document|header[0-9]*|footer[0-9]*|footnotes|endnotes)\.xml$`)

func readWordDocumentText(filePath string) (string, bool) {
	entries, isRead := readZipEntries(filePath, wordPartPattern.MatchString)
	body, hasBody := entries["word/document.xml"]
	if !isRead || !hasBody {
		return "", false
	}
	parts := []string{wordPartText(body)}
	for _, name := range sortedNames(entries) {
		if name == "word/document.xml" {
			continue
		}
		if text := wordPartText(entries[name]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), true
}

func wordPartText(content []byte) string {
	return ooxmlText(content, ooxmlLayout{TextElement: "t", Breaks: map[string]string{"tab": "\t", "br": "\n", "cr": "\n"}, Closings: map[string]string{"p": "\n", "tc": " | ", "tr": "\n"}, CellElement: "tc"})
}

type ooxmlLayout struct {
	TextElement string
	Breaks      map[string]string
	Closings    map[string]string
	CellElement string
}

func ooxmlText(content []byte, layout ooxmlLayout) string {
	decoder := xml.NewDecoder(strings.NewReader(string(content)))
	builder := strings.Builder{}
	isInText := false
	cellDepth := 0
	for {
		token, errorValue := decoder.Token()
		if errorValue != nil {
			break
		}
		switch typed := token.(type) {
		case xml.StartElement:
			isInText = typed.Name.Local == layout.TextElement
			if typed.Name.Local == layout.CellElement {
				cellDepth++
			}
			builder.WriteString(layout.Breaks[typed.Name.Local])
		case xml.EndElement:
			isInText = false
			builder.WriteString(closingText(layout, typed.Name.Local, cellDepth))
			if typed.Name.Local == layout.CellElement {
				cellDepth--
			}
		case xml.CharData:
			if isInText {
				builder.Write(typed)
			}
		}
	}
	return collapseBlankRuns(builder.String())
}

func closingText(layout ooxmlLayout, element string, cellDepth int) string {
	if cellDepth > 0 && layout.Closings[element] == "\n" {
		return " "
	}
	return layout.Closings[element]
}

func sortedNames(entries map[string][]byte) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var slidePartPattern = regexp.MustCompile(`^ppt/slides/slide([0-9]+)\.xml$`)

func readPresentationText(filePath string) (string, bool) {
	entries, isRead := readZipEntries(filePath, slidePartPattern.MatchString)
	if !isRead || len(entries) == 0 {
		return "", false
	}
	names := sortedNames(entries)
	sort.SliceStable(names, func(left int, right int) bool {
		return partNumber(slidePartPattern, names[left]) < partNumber(slidePartPattern, names[right])
	})
	slides := []string{}
	for _, name := range names {
		text := ooxmlText(entries[name], ooxmlLayout{TextElement: "t", Breaks: map[string]string{"br": "\n"}, Closings: map[string]string{"p": "\n"}})
		slides = append(slides, "Slide "+strconv.Itoa(partNumber(slidePartPattern, name))+":\n"+text)
	}
	return strings.Join(slides, "\n"), true
}

func partNumber(pattern *regexp.Regexp, name string) int {
	match := pattern.FindStringSubmatch(name)
	if len(match) < 2 {
		return 0
	}
	number, _ := strconv.Atoi(match[1])
	return number
}

type workbookSheet struct {
	Name string
	Part string
}

func readWorkbookText(filePath string) (string, bool) {
	entries, isRead := readZipEntries(filePath, func(name string) bool {
		return strings.HasPrefix(name, "xl/") && strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels")
	})
	if !isRead {
		return "", false
	}
	sheets := workbookSheets(entries)
	if len(sheets) == 0 {
		return "", false
	}
	sharedStrings := workbookSharedStrings(entries["xl/sharedStrings.xml"])
	parts := []string{}
	for _, sheet := range sheets {
		parts = append(parts, "Sheet "+sheet.Name+":\n"+worksheetText(entries[sheet.Part], sharedStrings))
	}
	return strings.Join(parts, "\n"), true
}

func workbookSheets(entries map[string][]byte) []workbookSheet {
	targets := relationshipTargets(entries["xl/_rels/workbook.xml.rels"], "xl")
	var workbook struct {
		Sheets []struct {
			Name           string `xml:"name,attr"`
			RelationshipID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(entries["xl/workbook.xml"], &workbook) != nil {
		return nil
	}
	sheets := []workbookSheet{}
	for _, sheet := range workbook.Sheets {
		if part, isKnown := targets[sheet.RelationshipID]; isKnown {
			sheets = append(sheets, workbookSheet{Name: sheet.Name, Part: part})
		}
	}
	return sheets
}

func relationshipTargets(content []byte, baseDirectory string) map[string]string {
	var relationships struct {
		Relationships []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	targets := map[string]string{}
	if xml.Unmarshal(content, &relationships) != nil {
		return targets
	}
	for _, relationship := range relationships.Relationships {
		target := strings.TrimPrefix(relationship.Target, "/")
		if !strings.HasPrefix(target, baseDirectory+"/") {
			target = path.Join(baseDirectory, target)
		}
		targets[relationship.ID] = target
	}
	return targets
}

func workbookSharedStrings(content []byte) []string {
	var table struct {
		Items []struct {
			Text   string `xml:"t"`
			Pieces []struct {
				Text string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if xml.Unmarshal(content, &table) != nil {
		return nil
	}
	sharedValues := make([]string, 0, len(table.Items))
	for _, item := range table.Items {
		text := item.Text
		for _, piece := range item.Pieces {
			text += piece.Text
		}
		sharedValues = append(sharedValues, text)
	}
	return sharedValues
}

type worksheetCell struct {
	Reference   string `xml:"r,attr"`
	Type        string `xml:"t,attr"`
	Value       string `xml:"v"`
	Formula     string `xml:"f"`
	InlineValue string `xml:"is>t"`
}

func worksheetText(content []byte, sharedStrings []string) string {
	var worksheet struct {
		Rows []struct {
			Number string          `xml:"r,attr"`
			Cells  []worksheetCell `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if xml.Unmarshal(content, &worksheet) != nil {
		return ""
	}
	lines := []string{}
	for _, row := range worksheet.Rows {
		values := []string{}
		for _, cell := range row.Cells {
			if value := worksheetCellText(cell, sharedStrings); value != "" {
				values = append(values, cell.Reference+"="+value)
			}
		}
		if len(values) > 0 {
			lines = append(lines, strings.Join(values, " | "))
		}
	}
	return strings.Join(lines, "\n")
}

func worksheetCellText(cell worksheetCell, sharedStrings []string) string {
	value := strings.TrimSpace(cell.Value)
	switch cell.Type {
	case "s":
		index, errorValue := strconv.Atoi(value)
		if errorValue != nil || index < 0 || index >= len(sharedStrings) {
			return ""
		}
		value = sharedStrings[index]
	case "inlineStr":
		value = cell.InlineValue
	}
	value = strings.Join(strings.Fields(value), " ")
	if formula := strings.TrimSpace(cell.Formula); formula != "" {
		return strings.TrimSpace(value + " (=" + formula + ")")
	}
	return value
}
