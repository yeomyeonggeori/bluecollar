package acpagent

import (
	"errors"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const requesterPersonID = "bluecollar"

const (
	TaskRunMetaKey    = "bluecollar.dev/task-run"
	CheckpointMetaKey = "bluecollar.dev/checkpoint"
	TurnResultMetaKey = "bluecollar.dev/turn-result"
	SteerMethod       = "_bluecollar.dev/steer"
)

type Options struct {
	AgentName            string
	LanguageModels       agentcontract.TaskTierLanguageModels
	DecisionModel        model.DecisionModel
	LLMCallRepository    taskstate.LLMCallRepository
	Skills               Skills
	HostCheckedToolNames []string
}

type Skills struct {
	InstructionBundleLoader func() agentcontract.InstructionBundle
	Retriever               agentcontract.SkillRetriever
	PinnedSkillNames        []string
}

func (options Options) validate() error {
	if options.LanguageModels.Low == nil {
		return errors.New("acpagent needs a Low tier language model: it answers routing and approval wording and is every other tier's fallback")
	}
	return nil
}
