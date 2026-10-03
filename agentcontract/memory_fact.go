package agentcontract

import "time"

type MemoryFact struct {
	FactID          string    `json:"factID"`
	ScopeType       string    `json:"scopeType"`
	NamespaceID     string    `json:"namespaceID"`
	Content         string    `json:"content"`
	Score           float64   `json:"score"`
	SourceEpisodeID string    `json:"sourceEpisodeID"`
	SourceKind      string    `json:"sourceKind"`
	ValidAt         time.Time `json:"validAt"`
}

const (
	MemoryScopePerson    = "person"
	MemoryScopeWorkspace = "workspace"
	MemoryScopeCircle    = "circle"
)
