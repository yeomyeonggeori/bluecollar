package acpagent

import (
	"errors"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

const requesterPersonID = "bluecollar"

const (
	TaskRunMetaKey           = "bluecollar.dev/task-run"
	CheckpointMetaKey        = "bluecollar.dev/checkpoint"
	TurnResultMetaKey        = "bluecollar.dev/turn-result"
	TurnRequestMetaKey       = "bluecollar.dev/turn-request"
	InstructionBundleMetaKey = "bluecollar.dev/instruction-bundle"
	SteerMethod              = "_bluecollar.dev/steer"
)

type Options struct {
	AgentName         string
	LanguageModels    agentcontract.TaskTierLanguageModels
	DecisionModel     model.DecisionModel
	LLMCallRepository taskstate.LLMCallRepository
	Skills            Skills
}

type Skills struct {
	Retriever agentcontract.SkillRetriever
}

func (options Options) validate() error {
	if options.LanguageModels.Low == nil {
		return errors.New("acpagent needs a Low tier language model: it answers routing and approval wording and is every other tier's fallback")
	}
	return nil
}
