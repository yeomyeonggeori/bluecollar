package visualcheck

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const maximumRefusalRunes = 1500

var (
	sectionOpening = regexp.MustCompile(`(?i)<section[\s>]`)
	sectionClosing = regexp.MustCompile(`(?i)</section\s*>`)
	tablePattern   = regexp.MustCompile(`(?i)<table[\s>]`)
	imagePattern   = regexp.MustCompile(`(?i)<img[\s>]`)
	chartPattern   = regexp.MustCompile(`data-chart="([^"]*)"`)
)

type Repair struct {
	Edit    string `json:"edit"`
	Change  string `json:"change"`
	Section string `json:"-"`
}

type attempt struct {
	repair    Repair
	problem   string
	usage     model.Usage
	hasNoEdit bool
}

func (attempt attempt) isAccepted() bool {
	return attempt.problem == ""
}

type repairContext struct {
	Theme            any              `json:"theme"`
	ReviewerFindings []Finding        `json:"reviewerFindings"`
	MeasuredDefects  []MeasuredDefect `json:"measuredDefects"`
	Section          string           `json:"section"`
	RefusedLastTime  string           `json:"refusedLastTime,omitempty"`
	Edits            []editChoice     `json:"edits"`
}

type editChoice struct {
	ID          string `json:"id"`
	Operation   string `json:"operation"`
	Description string `json:"description"`
}

func fixSlide(ctx context.Context, provider model.LanguageModelProvider, deck Deck, fixer Fixer, slide Slide, assessment Assessment, sheet model.DecisionImage, refusal string) attempt {
	if len(slide.Edits) == 0 {
		return attempt{problem: "no edit is offered for this slide", hasNoEdit: true}
	}
	image, errorValue := deck.Image(ctx, slide.Image)
	if errorValue != nil {
		return attempt{problem: fmt.Sprintf("read the render: %v", errorValue)}
	}
	request, errorValue := repairRequest(fixer, slide, assessment, image, sheet, refusal)
	if errorValue != nil {
		return attempt{problem: errorValue.Error()}
	}
	response, errorValue := provider.GenerateStructuredResponse(ctx, request)
	if errorValue != nil {
		return attempt{problem: fmt.Sprintf("language model: %v", errorValue)}
	}
	var repair Repair
	if errorValue := json.Unmarshal([]byte(response.Content), &repair); errorValue != nil {
		return attempt{usage: response.Usage, problem: fmt.Sprintf("the repair is not the requested JSON: %v", errorValue)}
	}
	edit, isOffered := slide.edit(repair.Edit)
	if !isOffered {
		return attempt{repair: repair, usage: response.Usage, problem: fmt.Sprintf("the repair names edit %q, which this slide does not offer", repair.Edit)}
	}
	repair.Section = edit.Section
	return attempt{repair: repair, usage: response.Usage, problem: rejectionReason(slide.Section, repair.Section)}
}

func repairRequest(fixer Fixer, slide Slide, assessment Assessment, image []byte, sheet model.DecisionImage, refusal string) (model.StructuredResponseRequest, error) {
	payload, errorValue := json.Marshal(repairContext{
		Theme:            slide.State["theme"],
		ReviewerFindings: assessment.Findings,
		MeasuredDefects:  assessment.Measured,
		Section:          slide.Section,
		RefusedLastTime:  refusal,
		Edits:            slide.editChoices(),
	})
	if errorValue != nil {
		return model.StructuredResponseRequest{}, errorValue
	}
	parts := []model.MessagePart{
		{Type: "text", Text: string(payload)},
		{Type: "image", MimeType: imageMediaType, DataBase64: base64.StdEncoding.EncodeToString(image)},
	}
	if len(sheet.Data) > 0 {
		parts = append(parts, model.MessagePart{Type: "image", MimeType: sheet.MediaType, DataBase64: base64.StdEncoding.EncodeToString(sheet.Data)})
	}
	return model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: fixer.Instructions},
			{Role: "user", Parts: parts},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "slide_repair", Document: repairSchema(slide.Edits), IsStrictlyEnforced: true},
	}, nil
}

func repairSchema(edits []Edit) string {
	identifiers := make([]string, 0, len(edits))
	for _, edit := range edits {
		identifiers = append(identifiers, edit.ID)
	}
	document := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"edit", "change"},
		"properties": map[string]any{
			"edit":   map[string]any{"type": "string", "enum": identifiers},
			"change": map[string]any{"type": "string"},
		},
	}
	encoded, _ := json.Marshal(document)
	return string(encoded)
}

func rejectionReason(original string, rewrite string) string {
	if !isOneSection(rewrite) {
		return "the rewrite is not exactly one section element"
	}
	if strings.TrimSpace(rewrite) == strings.TrimSpace(original) {
		return "the rewrite changes nothing"
	}
	if !maps.Equal(contentKinds(original), contentKinds(rewrite)) {
		return "the rewrite does not keep the original's tables, images and charts"
	}
	return ""
}

func isOneSection(source string) bool {
	trimmed := strings.TrimSpace(source)
	opening := sectionOpening.FindAllStringIndex(trimmed, -1)
	closing := sectionClosing.FindAllStringIndex(trimmed, -1)
	if len(opening) != 1 || len(closing) != 1 {
		return false
	}
	return opening[0][0] == 0 && closing[0][1] == len(trimmed)
}

func contentKinds(section string) map[string]int {
	kinds := map[string]int{}
	kinds["table"] = len(tablePattern.FindAllString(section, -1))
	kinds["image"] = len(imagePattern.FindAllString(section, -1))
	for _, match := range chartPattern.FindAllStringSubmatch(section, -1) {
		kinds["chart:"+match[1]]++
	}
	return kinds
}
