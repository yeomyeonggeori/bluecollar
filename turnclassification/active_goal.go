package turnclassification

import (
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const ExpectedResultTypeMessage = "message"

func NormalizeExpectedResults(results []agentcontract.ExpectedResult) []agentcontract.ExpectedResult {
	normalizedResults := []agentcontract.ExpectedResult{}
	seenResults := map[string]bool{}
	for _, result := range results {
		normalizedResult := normalizeExpectedResult(result, len(normalizedResults)+1)
		if strings.TrimSpace(normalizedResult.Description) == "" {
			continue
		}
		key := normalizedResult.Type + "\x00" + normalizedResult.Description
		if seenResults[key] {
			continue
		}
		seenResults[key] = true
		normalizedResults = append(normalizedResults, normalizedResult)
	}
	return foldMessageResultsIntoTheReply(normalizedResults)
}

func foldMessageResultsIntoTheReply(results []agentcontract.ExpectedResult) []agentcontract.ExpectedResult {
	foldedResults := []agentcontract.ExpectedResult{}
	replyIndex := -1
	for _, result := range results {
		if result.Type != ExpectedResultTypeMessage {
			foldedResults = append(foldedResults, result)
			continue
		}
		if replyIndex < 0 {
			replyIndex = len(foldedResults)
			foldedResults = append(foldedResults, result)
			continue
		}
		reply := &foldedResults[replyIndex]
		if !strings.Contains(reply.Description, result.Description) {
			reply.Description = reply.Description + " " + result.Description
		}
		reply.AcceptanceHints = toolcontract.AppendUniqueStrings(reply.AcceptanceHints, result.AcceptanceHints...)
		reply.Required = reply.Required || result.Required
	}
	return foldedResults
}

func normalizeExpectedResult(result agentcontract.ExpectedResult, index int) agentcontract.ExpectedResult {
	result.ID = strings.TrimSpace(result.ID)
	if result.ID == "" {
		result.ID = "result-" + strconv.Itoa(index)
	}
	result.Type = normalizeExpectedResultType(result.Type)
	result.Description = strings.TrimSpace(result.Description)
	result.AcceptanceHints = toolcontract.AppendUniqueStrings(result.AcceptanceHints)
	return result
}

func normalizeExpectedResultType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case agentcontract.ExpectedResultTypeFile:
		return agentcontract.ExpectedResultTypeFile
	case agentcontract.ExpectedResultTypeLink:
		return agentcontract.ExpectedResultTypeLink
	default:
		return ExpectedResultTypeMessage
	}
}
