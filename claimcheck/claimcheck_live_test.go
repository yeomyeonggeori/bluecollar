//go:build llmeval

package claimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/evaltest"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
)

const corpusVariable = "BLUECOLLAR_CLAIM_CORPUS"

type corpusDocument struct {
	Document     string          `json:"document"`
	Path         string          `json:"path"`
	Request      string          `json:"request"`
	Attachments  []Attachment    `json:"attachments"`
	RuntimeFacts json.RawMessage `json:"runtimeFacts"`
	Claims       []corpusClaim   `json:"claims"`
}

type corpusClaim struct {
	Claim
	Label     string `json:"label"`
	LabelName string `json:"labelName"`
}

type judgedDocument struct {
	Document    string             `json:"document"`
	Path        string             `json:"path"`
	Probability map[string]float64 `json:"claimProbability"`
	Kind        map[string]string  `json:"kind"`
	PromptTotal int64              `json:"promptTokens"`
	CostUSD     float64            `json:"costUSD"`
	corpus      corpusDocument
}

func TestLiveClaimKindsAgainstGradedDocuments(t *testing.T) {
	corpusPath := evaltest.RequireInput(t, corpusVariable, "point it at the claim-corpus.json exported from the graded office outputs")
	endpoint, errorValue := decisions.EndpointFromEnvironment()
	evaltest.RequireConfigured(t, "the decision model", errorValue)
	documents := readCorpus(t, corpusPath)
	judged := make([]judgedDocument, len(documents))
	var group sync.WaitGroup
	limiter := make(chan struct{}, 8)
	for index, document := range documents {
		group.Add(1)
		go func() {
			defer group.Done()
			limiter <- struct{}{}
			defer func() { <-limiter }()
			judged[index] = judgeCorpusDocument(t, endpoint, document)
		}()
	}
	group.Wait()
	for _, path := range corpusPaths(documents) {
		for _, threshold := range []float64{0.4, ClaimThreshold, 0.6} {
			t.Log(accuracyLine(judged, path, threshold))
		}
	}
	t.Log(costLine(judged))
	if output := os.Getenv("BLUECOLLAR_CLAIM_RESULTS"); output != "" {
		writeResults(t, output, judged)
	}
}

func readCorpus(t *testing.T, corpusPath string) []corpusDocument {
	t.Helper()
	content, errorValue := os.ReadFile(corpusPath)
	if errorValue != nil {
		t.Fatalf("read %s: %v", corpusPath, errorValue)
	}
	var corpus struct {
		Documents []corpusDocument `json:"documents"`
	}
	if errorValue := json.Unmarshal(content, &corpus); errorValue != nil || len(corpus.Documents) == 0 {
		t.Fatalf("%s holds no documents: %v", corpusPath, errorValue)
	}
	return corpus.Documents
}

func corpusPaths(documents []corpusDocument) []string {
	paths := []string{}
	for _, document := range documents {
		if !slices.Contains(paths, document.Path) {
			paths = append(paths, document.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

func judgeCorpusDocument(t *testing.T, endpoint decisions.Endpoint, document corpusDocument) judgedDocument {
	result := judgedDocument{Document: document.Document, Path: document.Path, Probability: map[string]float64{}, Kind: map[string]string{}, corpus: document}
	claims := []Claim{}
	for _, claim := range document.Claims {
		claims = append(claims, claim.Claim)
	}
	if len(claims) == 0 {
		return result
	}
	sources := Sources{Request: []string{document.Request}, Attachments: document.Attachments, RuntimeFacts: document.RuntimeFacts}
	judgment, errorValue := Judge(context.Background(), endpoint.DecisionModel(), sources, claims)
	if errorValue != nil {
		t.Errorf("%s: %v", document.Document, errorValue)
		return result
	}
	for _, verdict := range judgment.Verdicts {
		key := claimKey(verdict.Claim)
		result.Probability[key] = verdict.ClaimProbability
		result.Kind[key] = verdict.Kind
	}
	result.PromptTotal = judgment.Usage.PromptTokens
	result.CostUSD = judgment.Usage.CostUSD
	return result
}

func claimKey(claim Claim) string {
	return claim.Path + "\x00" + claim.Text
}

func accuracyLine(judged []judgedDocument, path string, threshold float64) string {
	graded, gradedCaught := map[string]bool{}, map[string]bool{}
	seeds, seedsCaught, clean, falseFlags, cleanDocuments, cleanDocumentsFlagged := 0, 0, 0, 0, 0, 0
	for _, document := range judged {
		if document.Path != path {
			continue
		}
		isClean, isFlagged := true, false
		for _, claim := range document.corpus.Claims {
			isHit := document.Probability[claimKey(claim.Claim)] >= threshold
			switch claim.Label {
			case "both":
				name := document.Document + "/" + claim.LabelName
				graded[name] = true
				gradedCaught[name] = gradedCaught[name] || isHit
				isClean = false
			case "seed":
				seeds++
				if isHit {
					seedsCaught++
				}
				isClean = false
			case "clean":
				clean++
				if isHit {
					falseFlags++
					isFlagged = true
				}
			default:
				isClean = false
			}
		}
		if isClean && !strings.HasSuffix(document.Document, "-seeded") {
			cleanDocuments++
			if isFlagged {
				cleanDocumentsFlagged++
			}
		}
	}
	caught := 0
	for name := range graded {
		if gradedCaught[name] {
			caught++
		}
	}
	return fmt.Sprintf("%s threshold %.2f: graded inventions caught %d/%d, seeded caught %d/%d, false flags %d/%d clean claims, clean documents flagged %d/%d",
		path, threshold, caught, len(graded), seedsCaught, seeds, falseFlags, clean, cleanDocumentsFlagged, cleanDocuments)
}

func costLine(judged []judgedDocument) string {
	prompts := []int64{}
	total := 0.0
	for _, document := range judged {
		if document.PromptTotal > 0 {
			prompts = append(prompts, document.PromptTotal)
		}
		total += document.CostUSD
	}
	sort.Slice(prompts, func(left, right int) bool { return prompts[left] < prompts[right] })
	if len(prompts) == 0 {
		return "no decision calls"
	}
	return fmt.Sprintf("%d documents asked, median prompt %d tokens, total cost USD %.5f", len(prompts), prompts[len(prompts)/2], total)
}

func writeResults(t *testing.T, output string, judged []judgedDocument) {
	t.Helper()
	content, errorValue := json.MarshalIndent(judged, "", " ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(output, content, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
}
