package toolcontract

import "testing"

func TestCanonicalToolName(t *testing.T) {
	testCases := []struct {
		name         string
		recordedName string
		expectedName string
	}{
		{name: "legacy shell", recordedName: "shell", expectedName: BashToolName},
		{name: "legacy file write", recordedName: "file_write", expectedName: WriteToolName},
		{name: "legacy file edit", recordedName: "file_edit", expectedName: EditToolName},
		{name: "legacy tool search", recordedName: "find_tools", expectedName: EquipToolName},
		{name: "current bash", recordedName: BashToolName, expectedName: BashToolName},
		{name: "current write", recordedName: WriteToolName, expectedName: WriteToolName},
		{name: "current edit", recordedName: EditToolName, expectedName: EditToolName},
		{name: "current equip", recordedName: EquipToolName, expectedName: EquipToolName},
		{name: "unchanged extension", recordedName: "calendar_list", expectedName: "calendar_list"},
		{name: "trimmed legacy name", recordedName: " shell ", expectedName: BashToolName},
		{name: "trimmed current name", recordedName: " bash ", expectedName: BashToolName},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actualName := CanonicalToolName(testCase.recordedName); actualName != testCase.expectedName {
				t.Fatalf("CanonicalToolName(%q) = %q, want %q", testCase.recordedName, actualName, testCase.expectedName)
			}
		})
	}
}

func TestEveryKernelToolNameSaysWhetherTheModelMayCallIt(t *testing.T) {
	listNameByToolName := map[string]string{}
	for _, toolName := range ModelFacingKernelToolNames() {
		listNameByToolName[toolName] = "ModelFacingKernelToolNames"
	}
	for _, toolName := range RuntimeOnlyKernelToolNames() {
		if listName, isListed := listNameByToolName[toolName]; isListed {
			t.Fatalf("%s is in %s and in RuntimeOnlyKernelToolNames, so nothing says whether the model may call it", toolName, listName)
		}
		listNameByToolName[toolName] = "RuntimeOnlyKernelToolNames"
	}
	for _, toolName := range KernelToolNames() {
		if _, isListed := listNameByToolName[toolName]; !isListed {
			t.Fatalf("%s is a kernel tool and neither list claims it; add it to ModelFacingKernelToolNames or to RuntimeOnlyKernelToolNames", toolName)
		}
	}
	if len(listNameByToolName) != len(KernelToolNames()) {
		t.Fatalf("the two lists name %d tools while the kernel has %d", len(listNameByToolName), len(KernelToolNames()))
	}
}
