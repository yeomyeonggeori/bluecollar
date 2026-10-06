package decisionconfig

import (
	"fmt"
	"io"

	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/model/decisions"
)

var DecisionEnvironmentNames = decisions.EnvironmentNames{
	Endpoint: "BLUECOLLAR_DECISION_ENDPOINT",
	APIKey:   "BLUECOLLAR_DECISION_API_KEY",
	Model:    "BLUECOLLAR_DECISION_MODEL",
}

func ConfiguredDecisionModel(warnings io.Writer) model.DecisionModel {
	endpoint, errorValue := decisions.EndpointFromEnvironment(DecisionEnvironmentNames)
	if errorValue != nil {
		fmt.Fprintln(warnings, "no decision model:", errorValue)
		return nil
	}
	return endpoint.DecisionModel()
}
