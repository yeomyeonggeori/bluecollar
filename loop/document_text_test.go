package loop

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func writeZipDocument(t *testing.T, filePath string, entries map[string]string) {
	t.Helper()
	file, errorValue := os.Create(filePath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	for name, content := range entries {
		entry, errorValue := archive.Create(name)
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		if _, errorValue := entry.Write([]byte(content)); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	if errorValue := archive.Close(); errorValue != nil {
		t.Fatal(errorValue)
	}
}

const wordNamespace = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func wordDocumentEntries(body string) map[string]string {
	return map[string]string{
		"[Content_Types].xml":  `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/document.xml":    `<w:document ` + wordNamespace + `><w:body>` + body + `</w:body></w:document>`,
		"word/header1.xml":     `<w:hdr ` + wordNamespace + `><w:p><w:r><w:t>Lakeside Storage Ltd.</w:t></w:r></w:p></w:hdr>`,
		"word/styles.xml":      `<w:styles ` + wordNamespace + `><w:style><w:name w:val="ignored style name"/></w:style></w:styles>`,
		"docProps/core.xml":    `<core>ignored metadata</core>`,
		"word/_rels/doc.rels":  `<Relationships/>`,
		"word/fontTable.xml":   `<w:fonts ` + wordNamespace + `/>`,
		"word/settings.xml":    `<w:settings ` + wordNamespace + `/>`,
		"word/webSettings.xml": `<w:webSettings ` + wordNamespace + `/>`,
	}
}

func wordParagraph(runs ...string) string {
	built := "<w:p>"
	for _, run := range runs {
		built += "<w:r><w:t xml:space=\"preserve\">" + run + "</w:t></w:r>"
	}
	return built + "</w:p>"
}

func TestWordDocumentTextReadsParagraphsTablesAndHeaders(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "lease.docx")
	body := wordParagraph("Monthly rent: ", "KRW 1,850,000") +
		`<w:tbl><w:tr><w:tc>` + wordParagraph("Deposit") + `</w:tc><w:tc>` + wordParagraph("KRW 20,000,000") + `</w:tc></w:tr></w:tbl>` +
		wordParagraph("임대 기간은 24개월로 한다.")
	writeZipDocument(t, filePath, wordDocumentEntries(body))

	text, isRead := readWordDocumentText(filePath)

	if !isRead {
		t.Fatal("expected the docx to be read")
	}
	for _, want := range []string{"Monthly rent: KRW 1,850,000", "Deposit | KRW 20,000,000", "임대 기간은 24개월로 한다.", "Lakeside Storage Ltd."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
	if strings.Contains(text, "ignored") {
		t.Fatalf("expected only document text, got %q", text)
	}
}

func TestWorkbookTextReadsEverySheetWithSharedStringsAndFormulas(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "order.xlsx")
	writeZipDocument(t, filePath, map[string]string{
		"xl/workbook.xml":            `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Lines" sheetId="1" r:id="rId1"/><sheet name="요약" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="/xl/worksheets/sheet2.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Pallet jack</t></si><si><r><t>합계</t></r><r><t> 금액</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml":   `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1"><v>14</v></c><c r="C1"><f>B1*215000</f><v>3010000</v></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml":   `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>1</v></c><c r="B1" t="inlineStr"><is><t>see Lines</t></is></c></row></sheetData></worksheet>`,
	})

	text, isRead := readWorkbookText(filePath)

	if !isRead {
		t.Fatal("expected the xlsx to be read")
	}
	for _, want := range []string{"Sheet Lines:", "A1=Pallet jack | B1=14 | C1=3010000 (=B1*215000)", "Sheet 요약:", "A1=합계 금액 | B1=see Lines"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
}

func TestPresentationTextReadsSlidesInNumericOrder(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "plan.pptx")
	slide := func(text string) string {
		return `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:sld>`
	}
	writeZipDocument(t, filePath, map[string]string{
		"ppt/slides/slide10.xml": slide("Launch in week 12"),
		"ppt/slides/slide2.xml":  slide("Budget 48,000 USD"),
	})

	text, isRead := readPresentationText(filePath)

	if !isRead || strings.Index(text, "Budget 48,000 USD") > strings.Index(text, "Launch in week 12") {
		t.Fatalf("expected slide 2 before slide 10, got %q", text)
	}
}

func TestMarkupTextDropsTagsScriptsAndEntities(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "invite.html")
	if errorValue := os.WriteFile(filePath, []byte(`<html><style>p{}</style><script>var x=1;</script><p>Doors open at 18:30 &amp; dinner at 19:00</p><div>Venue: Hall B</div></html>`), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}

	text, _ := readMarkupText(filePath)

	if text != "Doors open at 18:30 & dinner at 19:00\nVenue: Hall B" {
		t.Fatalf("unexpected markup text %q", text)
	}
}

func deliveredDocumentObservation(devicePath string, filename string) []turnObservation {
	return []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          toolcontract.FileDeliverToolName,
		Output:        toolcontract.ToolOutput{Content: "files delivered"},
		Effects:       []toolcontract.ResourceEffect{{ObjectType: "file", Effect: "attached", Path: devicePath}},
		Attachments:   []toolcontract.FileAttachment{{Filename: filename, DevicePath: devicePath}},
	}}
}

func TestTheChangeCheckReadsWhatADeliveredDocumentSays(t *testing.T) {
	workspaceRootPath := t.TempDir()
	documentsPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "documents")
	if errorValue := os.MkdirAll(documentsPath, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	writeZipDocument(t, filepath.Join(documentsPath, "lease.docx"), wordDocumentEntries(wordParagraph("Monthly rent: KRW 1,850,000")))
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}
	request := AgentTurnRequest{Prompt: "Draft the lease with a monthly rent of KRW 1,850,000 and attach it.", ToolSet: kernelFileToolSet(), WorkspaceRootPath: workspaceRootPath}
	change := expectedChange{Change: "file attached", Asked: request.Prompt}

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{change}, deliveredDocumentObservation("/workspace/private/people/person-1/documents/lease.docx", "lease.docx"))

	state, _ := json.Marshal(decisionModel.requests[0].State)
	if !strings.Contains(string(state), `"documents":[{"path":"/workspace/private/people/person-1/documents/lease.docx","text":"Monthly rent: KRW 1,850,000\nLakeside Storage Ltd."}]`) {
		t.Fatalf("expected the delivered document's text in the change check state, got %s", state)
	}
	instructions, _ := json.Marshal(decisionModel.requests[0].Questions)
	if !strings.Contains(string(instructions), "documents holds the text each changed file now carries") {
		t.Fatalf("expected the instruction to say how to read documents, got %s", instructions)
	}
}

func TestADocumentLongerThanTheBoundIsCutAndMarkedTruncated(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "minutes.md")
	if errorValue := os.WriteFile(filePath, []byte(strings.Repeat("가", documentTextMaximumRunes+40)), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}

	document, isRead := documentExtractOf("", filePath)

	if !isRead || !document.Truncated || len([]rune(document.Text)) != documentTextMaximumRunes {
		t.Fatalf("expected a %d-rune truncated extract, got %d runes truncated=%v", documentTextMaximumRunes, len([]rune(document.Text)), document.Truncated)
	}
}

func TestAMissingOrUnreadableDocumentAddsNoExtract(t *testing.T) {
	directory := t.TempDir()
	brokenPath := filepath.Join(directory, "broken.docx")
	if errorValue := os.WriteFile(brokenPath, []byte("not a zip"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}
	request := AgentTurnRequest{Prompt: "Attach the quote.", ToolSet: kernelFileToolSet(), WorkspaceRootPath: directory}
	observations := append(deliveredDocumentObservation(brokenPath, "broken.docx"), deliveredDocumentObservation(filepath.Join(directory, "gone.xlsx"), "gone.xlsx")...)

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{{Change: "file attached", Asked: request.Prompt}}, observations)

	state, _ := json.Marshal(decisionModel.requests[0].State)
	if strings.Contains(string(state), `"documents"`) {
		t.Fatalf("expected no documents for unreadable files, got %s", state)
	}
	instructions, _ := json.Marshal(decisionModel.requests[0].Questions)
	if strings.Contains(string(instructions), "documents holds") {
		t.Fatalf("expected no document instruction without documents, got %s", instructions)
	}
}
