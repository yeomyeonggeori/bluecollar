package loop

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func lastObservationFailed(observations []turnObservation) bool {
	if len(observations) == 0 {
		return false
	}
	return observations[len(observations)-1].Failed()
}

type turnObservation struct {
	ObservationID        string                        `json:"observationID"`
	Action               string                        `json:"action"`
	Tool                 string                        `json:"tool,omitempty"`
	ToolID               string                        `json:"toolID,omitempty"`
	ToolInput            json.RawMessage               `json:"toolInput,omitempty"`
	Output               toolcontract.ToolOutput       `json:"output,omitempty"`
	Effects              []toolcontract.ResourceEffect `json:"effects,omitempty"`
	Failure              *toolcontract.ToolFailure     `json:"failure,omitempty"`
	Summary              string                        `json:"summary,omitempty"`
	ImageRefs            []ToolResultImageRef          `json:"imageRefs,omitempty"`
	RepeatsObservationID string                        `json:"repeatsObservationID,omitempty"`
	ToolInputKey         string                        `json:"toolInputKey,omitempty"`
	AttemptFingerprint   string                        `json:"attemptFingerprint,omitempty"`
	RecoveryAttemptKey   string                        `json:"recoveryAttemptKey,omitempty"`
	AssistantText        string                        `json:"assistantText,omitempty"`
	ModelReasoning       string                        `json:"modelReasoning,omitempty"`
	ModelReasoningField  string                        `json:"modelReasoningField,omitempty"`
	RecoveryStep         string                        `json:"recoveryStep,omitempty"`
	ToolIsReadOnly       bool                          `json:"toolIsReadOnly,omitempty"`
	RecoveryAttemptSpent bool                          `json:"recoveryAttemptSpent,omitempty"`
	PolicyCode           string                        `json:"policyCode,omitempty"`
	RelatedResultIDs     []string                      `json:"relatedResultIDs,omitempty"`
	RelatedPaths         []string                      `json:"relatedPaths,omitempty"`
	RecoveryPacket       *RecoveryPacket               `json:"recoveryPacket,omitempty"`
	Attachments          []toolcontract.FileAttachment `json:"attachments,omitempty"`
	RecoveryActions      []toolcontract.RecoveryAction `json:"recoveryActions,omitempty"`
	ReplyNotes           []string                      `json:"replyNotes,omitempty"`
	ChangeCheck          *changeCheck                  `json:"changeCheck,omitempty"`
	DurationMS           int64                         `json:"durationMs"`
}

type ToolResultImageRef struct {
	ObservationID   string `json:"observationID"`
	AttachmentIndex int    `json:"attachmentIndex"`
	MimeType        string `json:"mimeType,omitempty"`
	Filename        string `json:"filename,omitempty"`
}

func (observation turnObservation) Failed() bool {
	return observation.Failure != nil
}

// Output.Data is what a result contract validates, so a tool that declares a schema
// cannot return without it. Output.Content is the text the model reads and is elided
// when the result is long, which is why nothing that wants fields should read it.
// The fallback carries the observations this loop writes itself, which have no
// contract and are never long enough to be elided.
func (observation turnObservation) StructuredOutput() []byte {
	if len(observation.Output.Data) > 0 {
		return observation.Output.Data
	}
	return []byte(observation.Output.Content)
}

func (observation turnObservation) ContentText() string {
	if strings.TrimSpace(observation.Output.Content) != "" {
		return observation.Output.Content
	}
	if len(observation.Output.Data) > 0 {
		return string(observation.Output.Data)
	}
	return ""
}

func (observation turnObservation) FailureCode() string {
	if observation.Failure == nil {
		return ""
	}
	return strings.TrimSpace(observation.Failure.Code)
}

func (observation turnObservation) FailureStage() string {
	if observation.Failure == nil {
		return ""
	}
	return strings.TrimSpace(observation.Failure.Stage)
}

func (observation turnObservation) FailureSummary() string {
	if observation.Failure == nil {
		return ""
	}
	return strings.TrimSpace(observation.Failure.UserSafeSummary)
}

func (observation turnObservation) Retryable() bool {
	return observation.Failure != nil && observation.Failure.Retryable
}

func (observation turnObservation) SafeRetry() bool {
	return observation.Failure != nil && observation.Failure.SafeRetry
}

func newContentObservation(observationID string, action string, tool string, content string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        strings.TrimSpace(action),
		Tool:          strings.TrimSpace(tool),
		Output:        toolcontract.ToolOutput{Content: strings.TrimSpace(content)},
	}
}

func newFailureObservation(observationID string, action string, tool string, content string, kind toolcontract.FailureKind, code toolcontract.FailureCode, stage string) turnObservation {
	observation := newContentObservation(observationID, action, tool, content)
	observation.Failure = &toolcontract.ToolFailure{
		Kind:            toolcontract.NormalizeFailureKind(kind),
		Code:            toolcontract.CanonicalFailureCode(code),
		Stage:           strings.TrimSpace(stage),
		UserSafeSummary: strings.TrimSpace(content),
	}
	return observation
}

func withObservationContent(observation turnObservation, content string) turnObservation {
	observation.Output.Content = strings.TrimSpace(content)
	return observation
}

func unreadableActionObservation(observations []turnObservation, actionError error) turnObservation {
	return newFailureObservation(
		nextObservationIDForObservations(observations),
		"policy",
		"",
		actionError.Error()+". Send the action again as a well-formed call.",
		toolcontract.FailureInvalidInput,
		toolcontract.FailureCodes.InvalidInput,
		"agent_action",
	)
}

func nextObservationID(index int) string {
	return fmt.Sprintf("obs-%03d", index)
}

func nextObservationIDForObservations(observations []turnObservation) string {
	return nextObservationID(nextObservationIndex(observations))
}

func (agentTurnRunner *AgentTurnRunner) nextUnusedObservationID(taskRunID string, observations []turnObservation) string {
	highestObservationIndex := highestRecordedObservationIndex(agentTurnRunner.taskRunService.ListTaskEvent(taskRunID))
	if inFlightIndex := nextObservationIndex(observations) - 1; inFlightIndex > highestObservationIndex {
		highestObservationIndex = inFlightIndex
	}
	return nextObservationID(highestObservationIndex + 1)
}

func highestRecordedObservationIndex(taskEvents []agentcontract.TaskEvent) int {
	highestObservationIndex := 0
	for _, taskEvent := range taskEvents {
		if !strings.HasPrefix(taskEvent.Name, agentcontract.ToolTaskEventPrefix) || !strings.HasSuffix(taskEvent.Name, agentcontract.ToolTaskEventResultSuffix) {
			continue
		}
		var observation struct {
			ObservationID string `json:"observationID"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &observation) != nil {
			continue
		}
		observationIndex, isValid := observationIndexFromID(observation.ObservationID)
		if isValid && observationIndex > highestObservationIndex {
			highestObservationIndex = observationIndex
		}
	}
	return highestObservationIndex
}

func observationIndexFromID(observationID string) (int, bool) {
	trimmedObservationID := strings.TrimSpace(observationID)
	if !strings.HasPrefix(trimmedObservationID, "obs-") {
		return 0, false
	}
	observationIndex, errorValue := strconv.Atoi(strings.TrimPrefix(trimmedObservationID, "obs-"))
	return observationIndex, errorValue == nil
}

func nextObservationIndex(observations []turnObservation) int {
	highestObservationIndex := 0
	for _, observation := range observations {
		observationIndex, isValid := observationIndexFromID(observation.ObservationID)
		if isValid && observationIndex > highestObservationIndex {
			highestObservationIndex = observationIndex
		}
	}
	return highestObservationIndex + 1
}
