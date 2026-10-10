package intake

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

var turnRouteGroups = [][]agentcontract.TurnRoute{
	{agentcontract.TurnRouteStartTask, agentcontract.TurnRouteContinueTask, agentcontract.TurnRouteReviseTask},
	{agentcontract.TurnRouteAnswerQuestion, agentcontract.TurnRouteAnswerMeta},
	{agentcontract.TurnRouteClarify},
	{agentcontract.TurnRouteGiveUp},
}

func routeByGroupedBelief(answer model.DecisionAnswer) agentcontract.TurnRoute {
	heaviestGroup := []agentcontract.TurnRoute{}
	heaviestWeight := 0.0
	for _, group := range turnRouteGroups {
		if weight := groupWeight(answer, group); weight > heaviestWeight {
			heaviestGroup, heaviestWeight = group, weight
		}
	}
	if len(heaviestGroup) == 0 {
		return agentcontract.TurnRoute(strings.TrimSpace(answer.Choice))
	}
	return likeliestRoute(answer, heaviestGroup)
}

func groupWeight(answer model.DecisionAnswer, group []agentcontract.TurnRoute) float64 {
	weight := 0.0
	for _, route := range group {
		weight += answer.ChoiceProbability(string(route))
	}
	return weight
}

func likeliestRoute(answer model.DecisionAnswer, group []agentcontract.TurnRoute) agentcontract.TurnRoute {
	likeliest := group[0]
	for _, route := range group[1:] {
		if answer.ChoiceProbability(string(route)) > answer.ChoiceProbability(string(likeliest)) {
			likeliest = route
		}
	}
	return likeliest
}
