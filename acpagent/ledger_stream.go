package acpagent

import (
	"sync"

	"github.com/yeomyeonggeori/blueprotocol/acpupdate"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type ledgerStream struct {
	mutex          sync.Mutex
	sentCounts     map[string]int
	uncarriedTails map[string][]agentcontract.LedgerRecord
}

func newLedgerStream() *ledgerStream {
	return &ledgerStream{sentCounts: map[string]int{}, uncarriedTails: map[string][]agentcontract.LedgerRecord{}}
}

func (stream *ledgerStream) send(rawTurnEvent taskstate.RawTurnEvent) agentcontract.LedgerRecord {
	stream.mutex.Lock()
	defer stream.mutex.Unlock()
	stream.sentCounts[rawTurnEvent.TaskRunID]++
	record := acpupdate.LedgerRecordOf(rawTurnEvent, stream.sentCounts[rawTurnEvent.TaskRunID])
	stream.uncarriedTails[rawTurnEvent.TaskRunID] = append(stream.uncarriedTails[rawTurnEvent.TaskRunID], record)
	return record
}

func (stream *ledgerStream) carry(taskRunID string) []agentcontract.LedgerRecord {
	stream.mutex.Lock()
	defer stream.mutex.Unlock()
	tail := stream.uncarriedTails[taskRunID]
	delete(stream.uncarriedTails, taskRunID)
	return tail
}
