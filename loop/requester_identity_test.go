package loop

import (
	"reflect"
	"testing"
)

func TestKernelRequestConversionPreservesTheCompleteRequester(t *testing.T) {
	request := AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{
			RequesterPersonID:       "sample-person-id",
			RequesterEmail:          "sample@example.com",
			RequesterName:           "이샘플",
			RequesterCallingName:    "샘플 님",
			RequesterHandle:         "sample",
			RequesterPlatformUserID: "sample-platform-id",
			RequesterCircles:        []string{"member", "sample-circle"},
		},
		Platform: "virtual",
	}
	converted := agentRequestFromTurnRequest(request)
	if !reflect.DeepEqual(converted.RequesterIdentity, request.RequesterIdentity) || converted.Platform != request.Platform {
		t.Fatalf("kernel conversion changed the requester: %+v", converted.RequesterIdentity)
	}
	converted.RequesterCircles[0] = "changed-circle"
	if request.RequesterCircles[0] != "member" {
		t.Fatal("kernel conversion must preserve the host's circle snapshot")
	}
}
