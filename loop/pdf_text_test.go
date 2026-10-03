package loop

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

type pdfFixture struct {
	t       *testing.T
	objects []string
}

func newPDFFixture(t *testing.T) *pdfFixture {
	return &pdfFixture{t: t, objects: []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}}
}

func (fixture *pdfFixture) add(body string) int {
	fixture.objects = append(fixture.objects, body)
	return len(fixture.objects)
}

func (fixture *pdfFixture) deflatedStream(dictionary string, content string) int {
	buffer := bytes.Buffer{}
	writer := zlib.NewWriter(&buffer)
	if _, errorValue := writer.Write([]byte(content)); errorValue != nil {
		fixture.t.Fatal(errorValue)
	}
	writer.Close()
	return fixture.add(fmt.Sprintf("<< %s /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", dictionary, buffer.Len(), buffer.String()))
}

func (fixture *pdfFixture) compositeFont(glyphs string, hasUnicode bool) int {
	mappings := strings.Builder{}
	for index, character := range []rune(glyphs) {
		fmt.Fprintf(&mappings, "<%04X> <%s>\n", index+1, utf16Hex(character))
	}
	descendant := fixture.add("<< /Type /Font /Subtype /CIDFontType2 /DW 1000 /W [1 [500 500]] >>")
	unicode := ""
	if hasUnicode {
		cmap := fmt.Sprintf("begincmap\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n%d beginbfchar\n%sendbfchar\nendcmap", len([]rune(glyphs)), mappings.String())
		unicode = fmt.Sprintf(" /ToUnicode %d 0 R", fixture.deflatedStream("", cmap))
	}
	return fixture.add(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /Encoding /Identity-H /DescendantFonts [%d 0 R]%s >>", descendant, unicode))
}

func (fixture *pdfFixture) pageWith(fonts string, content string) string {
	contents := fixture.deflatedStream("", content)
	page := fixture.add(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /Contents %d 0 R >>", contents))
	fixture.objects[1] = fmt.Sprintf("<< /Type /Pages /Count 1 /Kids [%d 0 R] /Resources << /Font << %s >> >> >>", page, fonts)
	built := strings.Builder{}
	built.WriteString("%PDF-1.7\n")
	for index, body := range fixture.objects {
		fmt.Fprintf(&built, "%d 0 obj\n%s\nendobj\n", index+1, body)
	}
	built.WriteString("trailer\n<< /Root 1 0 R >>\n%%EOF\n")
	filePath := filepath.Join(fixture.t.TempDir(), "fixture.pdf")
	if errorValue := os.WriteFile(filePath, []byte(built.String()), 0o600); errorValue != nil {
		fixture.t.Fatal(errorValue)
	}
	return filePath
}

func utf16Hex(character rune) string {
	hex := strings.Builder{}
	for _, unit := range utf16.Encode([]rune{character}) {
		fmt.Fprintf(&hex, "%04X", unit)
	}
	return hex.String()
}

func glyphCodes(glyphs string, text string) string {
	hex := strings.Builder{}
	runes := []rune(glyphs)
	for _, character := range text {
		for index, candidate := range runes {
			if candidate == character {
				fmt.Fprintf(&hex, "%04X", index+1)
			}
		}
	}
	return hex.String()
}

const fixtureGlyphs = "계약금 3,0원"

func TestPDFTextDecodesCompositeFontsThroughTheirUnicodeMap(t *testing.T) {
	fixture := newPDFFixture(t)
	korean := fixture.compositeFont(fixtureGlyphs, true)
	content := fmt.Sprintf("BT /F1 12 Tf 1 0 0 1 72 700 Tm <%s> Tj ET\nBT /F2 11 Tf 1 0 0 1 72 680 Tm [(Due) -300 (in 45 days)] TJ ET", glyphCodes(fixtureGlyphs, "계약금 3,000원"))
	filePath := fixture.pageWith(fmt.Sprintf("/F1 %d 0 R /F2 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", korean), content)

	text, isRead := readPDFText(filePath)

	if !isRead || text != "계약금 3,000원\nDue in 45 days" {
		t.Fatalf("unexpected pdf text %q (read=%v)", text, isRead)
	}
}

func TestPDFTextJoinsGlyphsPlacedOneByOneAndSpacesSeparateCells(t *testing.T) {
	fixture := newPDFFixture(t)
	korean := fixture.compositeFont(fixtureGlyphs, true)
	content := strings.Join([]string{
		fmt.Sprintf("BT /F1 10 Tf 1 0 0 1 100 500 Tm <%s> Tj ET", glyphCodes(fixtureGlyphs, "계")),
		fmt.Sprintf("BT /F1 10 Tf 1 0 0 1 105 500 Tm <%s> Tj ET", glyphCodes(fixtureGlyphs, "약")),
		fmt.Sprintf("BT /F1 10 Tf 1 0 0 1 110 500 Tm <%s> Tj ET", glyphCodes(fixtureGlyphs, "금")),
		fmt.Sprintf("BT /F1 10 Tf 1 0 0 1 200 500 Tm <%s> Tj ET", glyphCodes(fixtureGlyphs, "3,000원")),
	}, "\n")
	filePath := fixture.pageWith(fmt.Sprintf("/F1 %d 0 R", korean), content)

	text, _ := readPDFText(filePath)

	if text != "계약금 3,000원" {
		t.Fatalf("expected glyphs on one line joined and the far cell spaced, got %q", text)
	}
}

func TestPDFTextPrefersActualTextOverTheGlyphsItReplaces(t *testing.T) {
	fixture := newPDFFixture(t)
	content := "BT /F1 12 Tf 1 0 0 1 72 700 Tm /Span << /ActualText <FEFF" + utf16Hex('잔') + utf16Hex('금') + "> >> BDC (xx) Tj EMC ET"
	filePath := fixture.pageWith("/F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", content)

	text, _ := readPDFText(filePath)

	if text != "잔금" {
		t.Fatalf("expected the actual text, got %q", text)
	}
}

func TestAPDFWhoseTextCannotBeDecodedGivesNoExtract(t *testing.T) {
	fixture := newPDFFixture(t)
	korean := fixture.compositeFont(fixtureGlyphs, false)
	filePath := fixture.pageWith(fmt.Sprintf("/F1 %d 0 R", korean), fmt.Sprintf("BT /F1 12 Tf 1 0 0 1 72 700 Tm <%s> Tj ET", glyphCodes(fixtureGlyphs, "계약금")))

	if text, isRead := readPDFText(filePath); isRead {
		t.Fatalf("expected no extract for a font without a unicode map, got %q", text)
	}
}

func TestPDFTextReadsObjectsKeptInsideAnObjectStream(t *testing.T) {
	fixture := newPDFFixture(t)
	fontBody := "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	header := fmt.Sprintf("%d 0 ", len(fixture.objects)+2)
	fixture.deflatedStream(fmt.Sprintf("/Type /ObjStm /N 1 /First %d", len(header)), header+fontBody)
	filePath := fixture.pageWith(fmt.Sprintf("/F1 %d 0 R", len(fixture.objects)+1), "BT /F1 12 Tf 1 0 0 1 72 700 Tm (Net 30) Tj ET")

	text, _ := readPDFText(filePath)

	if text != "Net 30" {
		t.Fatalf("expected text through a font stored in an object stream, got %q", text)
	}
}

func FuzzPDFTextNeverPanicsOnMalformedInput(f *testing.F) {
	f.Add([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] >>\nendobj\n3 0 obj\n<< /Type /Page /Contents 4 0 R /Resources << /Font << /F1 << /Subtype /Type1 >> >> >> >>\nendobj\n4 0 obj\n<< /Length 30 >>\nstream\nBT /F1 9 Tf (a) Tj [(b) -9 (c)] TJ ET\nendstream\nendobj\n"))
	f.Add([]byte("%PDF-1.4\n1 0 obj << /Type /ObjStm /N 3 /First 2 >> stream\n1 0 endstream endobj 2 0 obj [1 0 R 2 0 R] endobj"))
	f.Fuzz(func(t *testing.T, content []byte) {
		pdfTextOf(content)
		parseUnicodeMap(content, 2)
	})
}
