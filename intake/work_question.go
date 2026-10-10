package intake

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func WorkQuestion(about string, agentName string) model.DecisionQuestion {
	return model.ChoiceQuestion{
		Instructions: about + "Does it ask " + agentName + " to do work that takes tools and time, and how much? Work asked of somebody else is none.",
		OptionDescriptions: map[string]string{
			string(agentcontract.WorkNone):       "nothing for " + agentName + " to do. Words alone answer it, from what is visible, common knowledge or judgment, including a translation, an explanation or a draft written in the reply; or nobody asked " + agentName + " for anything",
			string(agentcontract.WorkEasy):       "ordinary bounded work with a clear short outcome and one final reply, even when it takes a few tools: a lookup, a record, a change",
			string(agentcontract.WorkNormal):     "multi-step work, research, or a document or file to produce, where progress updates are useful",
			string(agentcontract.WorkHard):       "long, wide, deployment-shaped or verification-heavy work",
			string(agentcontract.WorkImpossible): "work that cannot be done: physically impossible, nonsensical, or plainly improper on its face. Never for a permission concern or a tool " + agentName + " might lack, which the work itself finds out",
		},
	}.Question()
}

func ReadWork(answer model.DecisionAnswer) agentcontract.Work {
	if len(answer.Probabilities) == 0 {
		return agentcontract.NormalizeWork(strings.TrimSpace(answer.Choice))
	}
	doableWeight := weightOf(answer, agentcontract.DoableWorkNames)
	impossibleWeight := answer.ChoiceProbability(string(agentcontract.WorkImpossible))
	if doableWeight+impossibleWeight <= answer.ChoiceProbability(string(agentcontract.WorkNone)) {
		return agentcontract.WorkNone
	}
	if impossibleWeight > doableWeight {
		return agentcontract.WorkImpossible
	}
	return likeliestWork(answer)
}

func weightOf(answer model.DecisionAnswer, optionNames []string) float64 {
	weight := 0.0
	for _, optionName := range optionNames {
		weight += answer.ChoiceProbability(optionName)
	}
	return weight
}

func likeliestWork(answer model.DecisionAnswer) agentcontract.Work {
	likeliest := agentcontract.WorkEasy
	for _, optionName := range agentcontract.DoableWorkNames {
		if answer.ChoiceProbability(optionName) > answer.ChoiceProbability(string(likeliest)) {
			likeliest = agentcontract.Work(optionName)
		}
	}
	return likeliest
}
