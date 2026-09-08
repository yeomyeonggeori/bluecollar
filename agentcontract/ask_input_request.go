package agentcontract

const AskKindInput = "ask_input"

type AskInputRequest struct {
	Kind                 string                `json:"kind"`
	Question             string                `json:"question"`
	Message              string                `json:"message"`
	Options              []ClarificationOption `json:"options,omitempty"`
	RecommendedOptionKey string                `json:"recommendedOptionKey,omitempty"`
	SelectionMode        string                `json:"selectionMode,omitempty"`
	ResponseLanguage     string                `json:"responseLanguage"`
}

func NewAskInputRequest(question string, options []ClarificationOption, responseLanguage string) AskInputRequest {
	request := AskInputRequest{
		Kind:             AskKindInput,
		Question:         question,
		Message:          question,
		Options:          options,
		ResponseLanguage: responseLanguage,
	}
	if len(options) == 0 {
		return request
	}
	request.RecommendedOptionKey = options[0].Key
	request.SelectionMode = "single"
	return request
}
