package acpagent

import (
	"io"

	acp "github.com/coder/acp-go-sdk"
)

func Serve(options Options, output io.Writer, input io.Reader) error {
	runningAgent, errorValue := New(options)
	if errorValue != nil {
		return errorValue
	}
	connection := acp.NewAgentSideConnection(runningAgent, output, input)
	runningAgent.Connect(connection)
	<-connection.Done()
	return nil
}
