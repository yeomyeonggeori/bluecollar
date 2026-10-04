package toolcontract

import "encoding/json"

const FileHoldsMaximumBytes = 16000

func FileHoldsOf(holds any) (json.RawMessage, bool) {
	document, errorValue := json.Marshal(holds)
	if errorValue != nil || len(document) > FileHoldsMaximumBytes || string(document) == "null" || string(document) == "{}" {
		return nil, false
	}
	return document, true
}
