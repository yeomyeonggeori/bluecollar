package visualcheck

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const cleanOption = "none"

func sampleOptions() map[string]string {
	return map[string]string{
		cleanOption:        "clean",
		"crowded":          "packed densely",
		"unreadable_chart": "chart squashed",
	}
}

func sampleManifest(rounds int, sections ...string) Manifest {
	manifest := Manifest{
		Question:  Question{Instructions: "Which defect?", Options: sampleOptions(), CleanOption: cleanOption},
		Threshold: 0.3,
		Rounds:    rounds,
		Fixer:     Fixer{Instructions: "Repair the slide.", KitGuide: "layouts: split"},
	}
	for index, section := range sections {
		manifest.Slides = append(manifest.Slides, Slide{Number: index + 1, Section: section, State: map[string]any{"theme": "corporate"}})
	}
	return manifest
}

type fakeDeck struct {
	mutex    sync.Mutex
	manifest Manifest
	renders  map[string]string
	versions int
	rebuilds []map[int]string
	failure  error
}

func newFakeDeck(manifest Manifest) *fakeDeck {
	deck := &fakeDeck{manifest: manifest, renders: map[string]string{}}
	deck.render()
	return deck
}

func (deck *fakeDeck) render() {
	deck.versions++
	for index := range deck.manifest.Slides {
		slide := &deck.manifest.Slides[index]
		slide.Image = slide.Section + "#" + string(rune('a'+deck.versions))
		deck.renders[slide.Image] = slide.Section
	}
}

func (deck *fakeDeck) Manifest(context.Context) (Manifest, error) { return deck.manifest, nil }

func (deck *fakeDeck) Image(_ context.Context, path string) ([]byte, error) {
	deck.mutex.Lock()
	defer deck.mutex.Unlock()
	return []byte(deck.renders[path]), nil
}

func (deck *fakeDeck) Rebuild(_ context.Context, replacements map[int]string) (Manifest, error) {
	deck.mutex.Lock()
	defer deck.mutex.Unlock()
	if deck.failure != nil {
		return Manifest{}, deck.failure
	}
	deck.rebuilds = append(deck.rebuilds, replacements)
	slides := slices.Clone(deck.manifest.Slides)
	for index := range slides {
		if section, isReplaced := replacements[slides[index].Number]; isReplaced {
			slides[index].Section = section
		}
	}
	deck.manifest.Slides = slides
	deck.render()
	return deck.manifest, nil
}

type fakeDecisionModel struct {
	mutex    sync.Mutex
	requests []model.DecisionRequest
	answer   func(section string) map[string]float64
}

func (fake *fakeDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	fake.mutex.Lock()
	fake.requests = append(fake.requests, request)
	fake.mutex.Unlock()
	probabilities := fake.answer(string(request.Images[0].Data))
	return model.DecisionResponse{
		Answers: map[string]model.DecisionAnswer{questionKey: {Type: model.DecisionQuestionTypeChoice, Probabilities: probabilities}},
		Usage:   model.Usage{CostUSD: 0.5},
	}, nil
}

type fakeLanguageModel struct {
	mutex    sync.Mutex
	requests []model.StructuredResponseRequest
	rewrite  func(section string) Repair
}

func (fake *fakeLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("unused")
}

func (fake *fakeLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	fake.mutex.Lock()
	fake.requests = append(fake.requests, request)
	fake.mutex.Unlock()
	var payload repairContext
	if errorValue := json.Unmarshal([]byte(request.Messages[1].Parts[1].Text), &payload); errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	content, errorValue := json.Marshal(fake.rewrite(payload.Section))
	return model.StructuredResponse{Content: string(content), Usage: model.Usage{TotalTokens: 10}}, errorValue
}

func section(body string) string {
	return "<section data-layout=\"split\">" + body + "</section>"
}

func distributionBySuffix(section string) map[string]float64 {
	switch {
	case strings.Contains(section, "BAD"):
		return map[string]float64{cleanOption: 0.6, "unreadable_chart": 0.35, "crowded": 0.05}
	default:
		return map[string]float64{cleanOption: 0.95, "crowded": 0.05}
	}
}

func appendedBody(original string, addition string) string {
	return strings.Replace(original, "</section>", addition+"</section>", 1)
}

func TestFlagsOnAnyNonCleanOptionAtThresholdEvenWhenCleanIsTop(t *testing.T) {
	deck := newFakeDeck(sampleManifest(0, section("BAD"), section("fine")))
	decisions := &fakeDecisionModel{answer: distributionBySuffix}
	report, errorValue := Run(context.Background(), decisions, &fakeLanguageModel{}, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !slices.Equal(report.Leftovers, []int{1}) {
		t.Fatalf("leftovers %v, want [1]", report.Leftovers)
	}
	if got := report.Slides[0].Findings; len(got) != 1 || got[0].Kind != "unreadable_chart" || got[0].Meaning != "chart squashed" {
		t.Fatalf("findings %+v", got)
	}
	if report.Usage.Decision.CostUSD != 1.0 {
		t.Fatalf("decision cost %v, want 1.0", report.Usage.Decision.CostUSD)
	}
}

func TestMeasuredDefectFlagsASlideTheReviewerCalledClean(t *testing.T) {
	manifest := sampleManifest(0, section("fine"))
	manifest.Slides[0].Measured = []MeasuredDefect{{Code: "text-overflow", Message: "overflows"}}
	report, _ := Run(context.Background(), &fakeDecisionModel{answer: distributionBySuffix}, &fakeLanguageModel{}, newFakeDeck(manifest))
	if !slices.Equal(report.Leftovers, []int{1}) || report.Slides[0].Measured[0] != "text-overflow" {
		t.Fatalf("report %+v", report)
	}
}

func TestRequestCarriesTheImageAndTheManifestQuestionVerbatim(t *testing.T) {
	deck := newFakeDeck(sampleManifest(0, section("fine")))
	decisions := &fakeDecisionModel{answer: distributionBySuffix}
	if _, errorValue := Run(context.Background(), decisions, &fakeLanguageModel{}, deck); errorValue != nil {
		t.Fatal(errorValue)
	}
	request := decisions.requests[0]
	want := model.ChoiceQuestion{Instructions: "Which defect?", OptionDescriptions: sampleOptions()}.Question()
	encodedWant, _ := json.Marshal(want)
	encodedGot, _ := json.Marshal(request.Questions[questionKey])
	if string(encodedWant) != string(encodedGot) || len(request.Questions) != 1 {
		t.Fatalf("question %s, want %s", encodedGot, encodedWant)
	}
	if len(request.Images) != 1 || request.Images[0].MediaType != "image/png" || string(request.Images[0].Data) != section("fine") {
		t.Fatalf("images %+v", request.Images)
	}
	if request.State.(map[string]any)["theme"] != "corporate" {
		t.Fatalf("state %v", request.State)
	}
}

func TestFixerRequestCarriesGuideFindingsAndImage(t *testing.T) {
	deck := newFakeDeck(sampleManifest(1, section("BAD")))
	languageModel := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: appendedBody(original, "<p>x</p>"), Change: "moved"}
	}}
	Run(context.Background(), &fakeDecisionModel{answer: distributionBySuffix}, languageModel, deck)
	request := languageModel.requests[0]
	if request.Messages[0].Role != "system" || request.Messages[0].Content != "Repair the slide." {
		t.Fatalf("system message %+v", request.Messages[0])
	}
	parts := request.Messages[1].Parts
	if parts[0].Text != "Kit guide:\nlayouts: split" || parts[2].Type != "image" || parts[2].MimeType != "image/png" || parts[2].DataBase64 == "" {
		t.Fatalf("parts %+v", parts)
	}
	var payload repairContext
	json.Unmarshal([]byte(parts[1].Text), &payload)
	if payload.Theme != "corporate" || payload.ReviewerFindings[0].Kind != "unreadable_chart" || payload.ReviewerFindings[0].Meaning != "chart squashed" {
		t.Fatalf("payload %+v", payload)
	}
	if !request.StructuredOutputSchema.IsStrictlyEnforced || !strings.Contains(request.StructuredOutputSchema.Document, `"additionalProperties":false`) {
		t.Fatalf("schema %+v", request.StructuredOutputSchema)
	}
}

func TestOnlyFixedSlidesAreReasked(t *testing.T) {
	deck := newFakeDeck(sampleManifest(3, section("fine one"), section("BAD"), section("fine three")))
	decisions := &fakeDecisionModel{answer: distributionBySuffix}
	languageModel := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: strings.Replace(original, "BAD", "good", 1), Change: "regrouped"}
	}}
	report, errorValue := Run(context.Background(), decisions, languageModel, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(decisions.requests) != 4 || len(languageModel.requests) != 1 {
		t.Fatalf("%d decision requests, %d fixer requests; want 4 and 1", len(decisions.requests), len(languageModel.requests))
	}
	if len(report.Leftovers) != 0 || len(report.Fixed) != 1 || report.Fixed[0] != (Fixed{Number: 2, Change: "regrouped"}) || report.RoundsUsed != 1 {
		t.Fatalf("report %+v", report)
	}
}

func TestNonImprovingFixIsRestoredAndNotTriedAgain(t *testing.T) {
	deck := newFakeDeck(sampleManifest(3, section("BAD")))
	languageModel := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: appendedBody(original, "<p>same</p>"), Change: "tried"}
	}}
	report, errorValue := Run(context.Background(), &fakeDecisionModel{answer: distributionBySuffix}, languageModel, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(languageModel.requests) != 1 || len(deck.rebuilds) != 2 {
		t.Fatalf("%d fixer requests, %d rebuilds; want 1 and 2", len(languageModel.requests), len(deck.rebuilds))
	}
	if deck.manifest.Slides[0].Section != section("BAD") {
		t.Fatalf("section not restored: %q", deck.manifest.Slides[0].Section)
	}
	if !slices.Equal(report.GivenUp, []int{1}) || !slices.Equal(report.Leftovers, []int{1}) || len(report.Fixed) != 0 || len(report.TextChanged) != 0 {
		t.Fatalf("report %+v", report)
	}
}

func TestRewriteThatTurnsATableIntoAChartIsRejected(t *testing.T) {
	original := section("BAD<table><tr><td>1</td></tr></table>")
	deck := newFakeDeck(sampleManifest(2, original))
	languageModel := &fakeLanguageModel{rewrite: func(string) Repair {
		return Repair{Section: section("BAD<div data-chart=\"bar\"></div>"), Change: "chart"}
	}}
	report, _ := Run(context.Background(), &fakeDecisionModel{answer: distributionBySuffix}, languageModel, deck)
	if len(deck.rebuilds) != 0 || len(report.Fixed) != 0 || !strings.Contains(report.Slides[0].Error, "tables, images and charts") {
		t.Fatalf("rebuilds %v, report %+v", deck.rebuilds, report)
	}
}

func TestRejectionReason(t *testing.T) {
	table := section("<table></table><img src=\"a\"><div data-chart=\"bar\"></div>")
	cases := map[string]struct {
		rewrite  string
		accepted bool
	}{
		"same parts reworded":     {section("<img src=\"a\"><div data-chart=\"bar\"></div><table></table><p>x</p>"), true},
		"table dropped":           {section("<img src=\"a\"><div data-chart=\"bar\"></div>"), false},
		"image added":             {section("<table></table><img><img><div data-chart=\"bar\"></div>"), false},
		"chart kind changed":      {section("<table></table><img src=\"a\"><div data-chart=\"line\"></div>"), false},
		"two sections":            {table + table, false},
		"text around the section": {"intro " + table, false},
		"unchanged":               {table, false},
	}
	for name, testCase := range cases {
		if got := rejectionReason(table, testCase.rewrite) == ""; got != testCase.accepted {
			t.Errorf("%s: accepted %v, want %v", name, got, testCase.accepted)
		}
	}
}

func TestRoundsCapStopsFixing(t *testing.T) {
	answer := func(section string) map[string]float64 {
		return map[string]float64{cleanOption: 0.5, "crowded": 0.9 - 0.1*float64(strings.Count(section, "+"))}
	}
	deck := newFakeDeck(sampleManifest(2, section("start")))
	languageModel := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: appendedBody(original, "+"), Change: "step"}
	}}
	report, errorValue := Run(context.Background(), &fakeDecisionModel{answer: answer}, languageModel, deck)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if report.RoundsUsed != 2 || len(languageModel.requests) != 2 || !slices.Equal(report.Leftovers, []int{1}) || len(report.GivenUp) != 0 {
		t.Fatalf("report %+v, %d fixer requests", report, len(languageModel.requests))
	}
}

func TestTextChangeIsReportedOnlyWhenVisibleWordsDiffer(t *testing.T) {
	deck := newFakeDeck(sampleManifest(1, section("<p data-x=\"BAD\">one</p>"), section("<p data-x=\"BAD\">two</p>")))
	languageModel := &fakeLanguageModel{rewrite: func(original string) Repair {
		if strings.Contains(original, "one") {
			return Repair{Section: section("<p data-x=\"ok\">one</p><aside class=\"notes\">new note</aside>"), Change: "notes only"}
		}
		return Repair{Section: section("<p data-x=\"ok\">three</p>"), Change: "reworded"}
	}}
	report, _ := Run(context.Background(), &fakeDecisionModel{answer: distributionBySuffix}, languageModel, deck)
	if !slices.Equal(report.TextChanged, []int{2}) || len(report.Fixed) != 2 {
		t.Fatalf("report %+v", report)
	}
}

func TestVisibleTextIgnoresTagsAttributesAndNotes(t *testing.T) {
	first := visibleText(section("<h2 class=\"a\">Sales   up</h2><aside class=\"notes\">hidden</aside><p>10&amp;20</p>"))
	second := visibleText(section("<p style=\"x\">Sales up</p><h2>10&20</h2><aside class=\"notes extra\">other</aside>"))
	if first != "Sales up 10&20" || first != second {
		t.Fatalf("%q vs %q", first, second)
	}
}

func TestRebuildFailureAbortsWithTheLastGoodReport(t *testing.T) {
	deck := newFakeDeck(sampleManifest(2, section("BAD")))
	deck.failure = errors.New("office failed")
	languageModel := &fakeLanguageModel{rewrite: func(original string) Repair {
		return Repair{Section: appendedBody(original, "<p>x</p>"), Change: "x"}
	}}
	report, errorValue := Run(context.Background(), &fakeDecisionModel{answer: distributionBySuffix}, languageModel, deck)
	if errorValue == nil || !strings.Contains(errorValue.Error(), "office failed") {
		t.Fatalf("error %v", errorValue)
	}
	if !slices.Equal(report.Leftovers, []int{1}) || len(report.Slides) != 1 {
		t.Fatalf("report %+v", report)
	}
}

func TestParseManifestValidates(t *testing.T) {
	valid := `{"question":{"instructions":"q","options":{"none":"clean","crowded":"c"},"cleanOption":"none"},"threshold":0.3,"rounds":3,"fixer":{"instructions":"f","kitGuide":"g"},"source":"/a","slides":[{"number":1,"image":"/a.png","state":{"theme":{}},"section":"<section></section>","measured":[]}]}`
	manifest, errorValue := ParseManifest([]byte(valid))
	if errorValue != nil || manifest.Slides[0].Number != 1 || manifest.Rounds != 3 {
		t.Fatalf("valid manifest: %+v %v", manifest, errorValue)
	}
	invalid := map[string]string{
		"missing clean option": strings.Replace(valid, `"cleanOption":"none"`, `"cleanOption":"other"`, 1),
		"threshold of one":     strings.Replace(valid, `"threshold":0.3`, `"threshold":1`, 1),
		"threshold of zero":    strings.Replace(valid, `"threshold":0.3`, `"threshold":0`, 1),
		"negative rounds":      strings.Replace(valid, `"rounds":3`, `"rounds":-1`, 1),
		"not json":             "{",
	}
	for name, content := range invalid {
		if _, errorValue := ParseManifest([]byte(content)); errorValue == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
