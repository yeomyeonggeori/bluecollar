package agentcontract

import "encoding/json"

const (
	LedgerMetaKey         = "blueprotocol/ledger"
	CarriedOutCallMetaKey = "blueprotocol/carried-out"
)

type LedgerRecord struct {
	Name string          `json:"name"`
	Body json.RawMessage `json:"body,omitempty"`
	Text string          `json:"text,omitempty"`
}

func (record LedgerRecord) EventBody() string {
	if record.Text != "" {
		return record.Text
	}
	return string(record.Body)
}
