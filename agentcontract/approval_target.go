package agentcontract

import "strings"

type ApprovalTarget struct {
	InputField string   `json:"inputField,omitempty"`
	ID         string   `json:"id,omitempty"`
	IDs        []string `json:"ids,omitempty"`
	Title      string   `json:"title,omitempty"`
	StartsAt   string   `json:"startsAt,omitempty"`
	Preview    string   `json:"preview,omitempty"`
}

func (target ApprovalTarget) IsResolved() bool {
	return strings.TrimSpace(target.InputField) != "" && (strings.TrimSpace(target.ID) != "" || len(target.IDs) > 0)
}
