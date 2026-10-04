package visualcheck

import (
	"context"
	"fmt"
	"sort"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	questionKey    = "visual_defect"
	imageMediaType = "image/png"
)

type Finding struct {
	Kind        string  `json:"kind"`
	Probability float64 `json:"probability"`
	Meaning     string  `json:"meaning"`
}

type Assessment struct {
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Findings      []Finding          `json:"findings,omitempty"`
	Measured      []MeasuredDefect   `json:"measured,omitempty"`
	Error         string             `json:"error,omitempty"`
	usage         model.Usage
}

func (assessment Assessment) IsFlagged() bool {
	return len(assessment.Findings) > 0 || len(assessment.Measured) > 0
}

func reviewSlides(ctx context.Context, decisionModel model.DecisionModel, deck Deck, manifest Manifest, slides []Slide) map[int]Assessment {
	assessments := inParallel(slides, func(slide Slide) Assessment {
		return reviewSlide(ctx, decisionModel, deck, manifest, slide)
	})
	byNumber := map[int]Assessment{}
	for index, slide := range slides {
		byNumber[slide.Number] = assessments[index]
	}
	return byNumber
}

func reviewSlide(ctx context.Context, decisionModel model.DecisionModel, deck Deck, manifest Manifest, slide Slide) Assessment {
	image, errorValue := deck.Image(ctx, slide.Image)
	if errorValue != nil {
		return failedAssessment(slide, fmt.Errorf("read the render: %w", errorValue))
	}
	response, errorValue := decisionModel.Decide(ctx, reviewRequest(manifest.Question, slide, image))
	if errorValue != nil {
		return failedAssessment(slide, fmt.Errorf("decision model: %w", errorValue))
	}
	answer, isAnswered := response.Answers[questionKey]
	if !isAnswered {
		return failedAssessment(slide, fmt.Errorf("the decision model gave no answer for %s", questionKey))
	}
	return Assessment{
		Probabilities: answer.Probabilities,
		Findings:      flaggedFindings(manifest, answer),
		Measured:      slide.Measured,
		usage:         response.Usage,
	}
}

func failedAssessment(slide Slide, errorValue error) Assessment {
	return Assessment{Measured: slide.Measured, Error: errorValue.Error()}
}

func reviewRequest(question Question, slide Slide, image []byte) model.DecisionRequest {
	return model.DecisionRequest{
		State: slide.State,
		Questions: map[string]model.DecisionQuestion{
			questionKey: model.ChoiceQuestion{Instructions: question.Instructions, OptionDescriptions: question.Options}.Question(),
		},
		Images: []model.DecisionImage{{MediaType: imageMediaType, Data: image}},
	}
}

func flaggedFindings(manifest Manifest, answer model.DecisionAnswer) []Finding {
	findings := []Finding{}
	for kind, meaning := range manifest.Question.Options {
		probability := answer.ChoiceProbability(kind)
		if kind != manifest.Question.CleanOption && probability >= manifest.Threshold {
			findings = append(findings, Finding{Kind: kind, Probability: probability, Meaning: meaning})
		}
	}
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Probability != findings[right].Probability {
			return findings[left].Probability > findings[right].Probability
		}
		return findings[left].Kind < findings[right].Kind
	})
	return findings
}
