package agentcontract

import "strings"

type InstructionSection struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type SystemInstruction struct {
	Sections []InstructionSection `json:"sections"`
}

func (systemInstruction SystemInstruction) Append(name string, body string) SystemInstruction {
	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return systemInstruction
	}
	systemInstruction.Sections = append(systemInstruction.Sections, InstructionSection{Name: name, Body: trimmedBody})
	return systemInstruction
}

func (systemInstruction SystemInstruction) Text() string {
	bodies := make([]string, 0, len(systemInstruction.Sections))
	for _, section := range systemInstruction.Sections {
		bodies = append(bodies, section.Body)
	}
	return strings.Join(bodies, "\n\n")
}

func (systemInstruction SystemInstruction) BytesBySection() map[string]int {
	if len(systemInstruction.Sections) == 0 {
		return nil
	}
	bytesBySection := map[string]int{}
	for _, section := range systemInstruction.Sections {
		bytesBySection[section.Name] += len(section.Body)
	}
	return bytesBySection
}
