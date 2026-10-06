package agentcontract

import "testing"

func TestATargetIsResolvedByOneIdentityOrByAListOfThem(t *testing.T) {
	for name, testCase := range map[string]struct {
		target     ApprovalTarget
		isResolved bool
	}{
		"one identity":       {ApprovalTarget{InputField: "personHint", ID: "person-1"}, true},
		"a list of identity": {ApprovalTarget{InputField: "personHints", IDs: []string{"person-1", "person-2"}}, true},
		"no field to narrow": {ApprovalTarget{IDs: []string{"person-1"}}, false},
		"no identity at all": {ApprovalTarget{InputField: "personHints"}, false},
		"only a preview":     {ApprovalTarget{Preview: "회식은 금요일입니다"}, false},
	} {
		if testCase.target.IsResolved() != testCase.isResolved {
			t.Errorf("%s: IsResolved() = %v, want %v", name, !testCase.isResolved, testCase.isResolved)
		}
	}
}
