package loop

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestActionSchemaCarriesNoNullValues(t *testing.T) {
	booleanValues := []bool{false, true}
	for _, allowQualityCriteria := range booleanValues {
		for _, hasFailureDebt := range booleanValues {
			for _, allowFail := range booleanValues {
				for _, allowFinish := range booleanValues {
					label := fmt.Sprintf("quality=%v debt=%v fail=%v finish=%v", allowQualityCriteria, hasFailureDebt, allowFail, allowFinish)
					schema := actionSchemaForToolSet(nil, nil, allowQualityCriteria, nil, hasFailureDebt, allowFail, allowFinish)
					var document any
					if unmarshalError := json.Unmarshal([]byte(schema), &document); unmarshalError != nil {
						t.Fatalf("%s: schema is not valid JSON: %v", label, unmarshalError)
					}
					for _, nullPath := range nullValuePaths(document, "$") {
						t.Errorf("%s: null value at %s", label, nullPath)
					}
				}
			}
		}
	}
}

func nullValuePaths(node any, path string) []string {
	switch typedNode := node.(type) {
	case nil:
		return []string{path}
	case map[string]any:
		paths := []string{}
		for key, value := range typedNode {
			paths = append(paths, nullValuePaths(value, path+"."+key)...)
		}
		return paths
	case []any:
		paths := []string{}
		for index, value := range typedNode {
			paths = append(paths, nullValuePaths(value, fmt.Sprintf("%s[%d]", path, index))...)
		}
		return paths
	}
	return nil
}
