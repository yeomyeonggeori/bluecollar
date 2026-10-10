package intake

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func TestWorkIsReadByWeighingNoneAgainstEverythingElse(t *testing.T) {
	testCases := []struct {
		name          string
		probabilities map[string]float64
		want          agentcontract.Work
	}{
		{"doable work split three ways outweighs a single none", map[string]float64{"none": 0.34, "easy": 0.31, "normal": 0.22, "hard": 0.05, "impossible": 0.08}, agentcontract.WorkEasy},
		{"none stands when it outweighs all the rest together", map[string]float64{"none": 0.55, "easy": 0.3, "normal": 0.15}, agentcontract.WorkNone},
		{"doable work split three ways outweighs a single impossible", map[string]float64{"none": 0.1, "impossible": 0.36, "easy": 0.24, "normal": 0.18, "hard": 0.12}, agentcontract.WorkEasy},
		{"impossible stands when it outweighs all doable work", map[string]float64{"none": 0.2, "impossible": 0.5, "easy": 0.3}, agentcontract.WorkImpossible},
		{"the likeliest doable level is the one taken", map[string]float64{"none": 0.2, "easy": 0.2, "normal": 0.45, "hard": 0.15}, agentcontract.WorkNormal},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			answer := model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "none", Probabilities: testCase.probabilities}
			if work := ReadWork(answer); work != testCase.want {
				t.Fatalf("expected %q, got %q", testCase.want, work)
			}
		})
	}
}

func TestWorkWithoutADistributionIsTheChoice(t *testing.T) {
	if work := ReadWork(model.DecisionAnswer{Choice: " hard "}); work != agentcontract.WorkHard {
		t.Fatalf("expected the bare choice, got %q", work)
	}
	if work := ReadWork(model.DecisionAnswer{Choice: "medium"}); work != "" {
		t.Fatalf("expected an unknown choice to read as nothing, got %q", work)
	}
}

func TestTheWorkQuestionOffersTheWorkVocabulary(t *testing.T) {
	criteria := criteriaText(t, WorkQuestion("About message m1. ", "인턴"))
	for _, name := range agentcontract.WorkNames {
		if !strings.Contains(criteria, `"`+name+`"`) {
			t.Fatalf("expected %q among the options, got %s", name, criteria)
		}
	}
}
