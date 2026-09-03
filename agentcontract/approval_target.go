package agentcontract

import "strings"

type ApprovalTarget struct {
	InputField string `json:"inputField,omitempty"`
	ID         string `json:"id,omitempty"`
	Title      string `json:"title,omitempty"`
	StartsAt   string `json:"startsAt,omitempty"`
	Preview    string `json:"preview,omitempty"`
}

func (target ApprovalTarget) IsResolved() bool {
	return strings.TrimSpace(target.InputField) != "" && strings.TrimSpace(target.ID) != ""
}
