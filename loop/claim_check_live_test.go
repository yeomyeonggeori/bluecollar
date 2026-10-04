//go:build llmeval

package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/evaltest"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const claimCorpusVariable = "BLUECOLLAR_CLAIM_CORPUS"

type claimCorpus struct {
	Documents []claimCorpusDocument `json:"documents"`
}

type claimCorpusDocument struct {
	Document     string              `json:"document"`
	Arm          string              `json:"arm"`
	Case         string              `json:"case"`
	Path         string              `json:"path"`
	Request      string              `json:"request"`
	Attachments  []attachmentPreview `json:"attachments"`
	RuntimeFacts any                 `json:"runtimeFacts"`
	Claims       []claimCorpusClaim  `json:"claims"`
}

type claimCorpusClaim struct {
	Path      string `json:"path"`
	At        string `json:"at"`
	Text      string `json:"text"`
	Label     string `json:"label"`
	LabelName string `json:"labelName"`
}

type claimVerdicts struct {
	document claimCorpusDocument
	noul     map[string]float64
	usage    model.Usage
}

func TestLiveClaimSupportAgainstGradedDocuments(t *testing.T) {
	corpusPath := evaltest.RequireInput(t, claimCorpusVariable, "point it at the claim-corpus.json exported from the graded office outputs")
	endpoint, errorValue := decisions.EndpointFromEnvironment()
	evaltest.RequireConfigured(t, "the decision model", errorValue)
	corpus := readClaimCorpus(t, corpusPath)
	results := make([]claimVerdicts, len(corpus.Documents))
	var group sync.WaitGroup
	limiter := make(chan struct{}, 8)
	for index, document := range corpus.Documents {
		group.Add(1)
		go func() {
			defer group.Done()
			limiter <- struct{}{}
			defer func() { <-limiter }()
			results[index] = judgeCorpusDocument(t, endpoint.DecisionModel(), document)
		}()
	}
	group.Wait()
	for _, threshold := range []float64{0.3, claimSupportedThreshold, 0.5} {
		t.Log(claimAccuracyLine(results, threshold))
	}
	t.Log(claimCostLine(results))
	if output := os.Getenv("BLUECOLLAR_CLAIM_RESULTS"); output != "" {
		writeClaimResults(t, output, results)
	}
}

func readClaimCorpus(t *testing.T, corpusPath string) claimCorpus {
	t.Helper()
	document, errorValue := os.ReadFile(corpusPath)
	if errorValue != nil {
		t.Fatalf("read %s: %v", corpusPath, errorValue)
	}
	var corpus claimCorpus
	if errorValue := json.Unmarshal(document, &corpus); errorValue != nil || len(corpus.Documents) == 0 {
		t.Fatalf("%s holds no documents: %v", corpusPath, errorValue)
	}
	return corpus
}

func judgeCorpusDocument(t *testing.T, decisionModel model.DecisionModel, document claimCorpusDocument) claimVerdicts {
	verdicts := claimVerdicts{document: document, noul: map[string]float64{}}
	if len(document.Claims) == 0 {
		return verdicts
	}
	request, observations := corpusDelivery(t, document)
	recorder := &usageRecorder{decisionModel: decisionModel}
	check, errorValue := checkExpectedChanges(context.Background(), recorder, request, []expectedChange{{Change: "file created", Asked: document.Request}}, observations, deliveredClaims(request, observations))
	if errorValue != nil {
		t.Errorf("%s: %v", document.Document, errorValue)
		return verdicts
	}
	for _, claim := range check.JudgedClaims {
		verdicts.noul[claim.Path+"\x00"+claim.Text] = claim.Noul
	}
	verdicts.usage = recorder.usage
	return verdicts
}

type usageRecorder struct {
	decisionModel model.DecisionModel
	usage         model.Usage
}

func (recorder *usageRecorder) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	response, errorValue := recorder.decisionModel.Decide(ctx, request)
	recorder.usage = response.Usage
	return response, errorValue
}

func corpusDelivery(t *testing.T, document claimCorpusDocument) (AgentTurnRequest, []turnObservation) {
	claims := []toolcontract.SourceClaim{}
	for _, claim := range document.Claims {
		claims = append(claims, toolcontract.SourceClaim{Path: claim.Path, At: claim.At, Text: claim.Text})
	}
	known, _ := json.Marshal(document.RuntimeFacts)
	filePath := "/home/bc_person_sample/documents/" + document.Document + ".pdf"
	attachment := toolcontract.FileAttachment{DevicePath: filePath, Filename: document.Document + ".pdf",
		Source: &toolcontract.DeliveredSource{IsLayoutOwnedByCode: document.Path != "sentences", Claims: claims, Known: known}}
	request := AgentTurnRequest{Prompt: document.Request, ToolSet: kernelFileToolSet()}
	for _, attachment := range document.Attachments {
		request.InputParts = append(request.InputParts, agentcontract.AgentPart{Type: agentcontract.AgentPartTypeFile, File: &agentcontract.AgentFilePart{Filename: attachment.Name, MarkdownPreview: attachment.Text}})
	}
	return request, []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          toolcontract.FileDeliverToolName,
		Output:        toolcontract.ToolOutput{Content: "files delivered"},
		Effects:       []toolcontract.ResourceEffect{{ObjectType: "file", Effect: "attached", Path: filePath}},
		Attachments:   []toolcontract.FileAttachment{attachment},
	}}
}

func claimAccuracyLine(results []claimVerdicts, threshold float64) string {
	caughtLabels, labels := map[string]bool{}, map[string]bool{}
	seedsCaught, seeds, falseFlags, cleanClaims := 0, 0, 0, 0
	flaggedCleanDocuments, cleanDocuments := 0, 0
	for _, result := range results {
		isCleanDocument, hasFalseFlag := true, false
		for _, claim := range result.document.Claims {
			noul, isJudged := result.noul[claim.Path+"\x00"+claim.Text]
			isFlagged := isJudged && noul < threshold
			switch claim.Label {
			case "both":
				key := result.document.Document + "\x00" + claim.LabelName
				labels[key] = true
				caughtLabels[key] = caughtLabels[key] || isFlagged
				isCleanDocument = false
			case "seed":
				seeds++
				if isFlagged {
					seedsCaught++
				}
				isCleanDocument = false
			case "clean":
				cleanClaims++
				if isFlagged {
					falseFlags++
					hasFalseFlag = true
				}
			default:
				isCleanDocument = false
			}
		}
		if isCleanDocument {
			cleanDocuments++
			if hasFalseFlag {
				flaggedCleanDocuments++
			}
		}
	}
	caught := 0
	for _, isCaught := range caughtLabels {
		if isCaught {
			caught++
		}
	}
	return fmt.Sprintf("threshold %.2f: graded inventions caught %d/%d, seeded caught %d/%d, false flags %d/%d clean claims, clean documents flagged %d/%d",
		threshold, caught, len(labels), seedsCaught, seeds, falseFlags, cleanClaims, flaggedCleanDocuments, cleanDocuments)
}

func claimCostLine(results []claimVerdicts) string {
	prompts := []int64{}
	var cost float64
	for _, result := range results {
		if result.usage.PromptTokens > 0 {
			prompts = append(prompts, result.usage.PromptTokens)
			cost += result.usage.CostUSD
		}
	}
	if len(prompts) == 0 {
		return "no decision calls"
	}
	sort.Slice(prompts, func(left, right int) bool { return prompts[left] < prompts[right] })
	return fmt.Sprintf("%d decision calls, median prompt %d tokens, total cost USD %.5f", len(prompts), prompts[len(prompts)/2], cost)
}

func writeClaimResults(t *testing.T, output string, results []claimVerdicts) {
	rows := []map[string]any{}
	for _, result := range results {
		rows = append(rows, map[string]any{"document": result.document.Document, "arm": result.document.Arm, "case": result.document.Case,
			"noul": result.noul, "promptTokens": result.usage.PromptTokens, "costUSD": result.usage.CostUSD})
	}
	document, _ := json.MarshalIndent(rows, "", " ")
	if errorValue := os.WriteFile(output, document, 0o600); errorValue != nil {
		t.Fatalf("write %s: %v", output, errorValue)
	}
}
