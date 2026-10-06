package acpagent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

const workingDirectorySkillsPath = ".agents/skills"

func instructionBundleLoaderFor(skills Skills, workingDirectory string) func() agentcontract.InstructionBundle {
	if skills.InstructionBundleLoader != nil {
		return skills.InstructionBundleLoader
	}
	return func() agentcontract.InstructionBundle {
		return workingDirectoryInstructionBundle(workingDirectory)
	}
}

func workingDirectoryInstructionBundle(workingDirectory string) agentcontract.InstructionBundle {
	skillFiles, _ := filepath.Glob(filepath.Join(workingDirectory, workingDirectorySkillsPath, "*", "SKILL.md"))
	bundle := agentcontract.InstructionBundle{}
	for _, skillFile := range skillFiles {
		skillInstruction, isReadable := skillInstructionOfFile(skillFile)
		if !isReadable {
			continue
		}
		bundle.Skills = append(bundle.Skills, skillInstruction)
		bundle.Sources = append(bundle.Sources, skillInstruction.Source)
	}
	return bundle
}

func skillInstructionOfFile(skillFile string) (agentcontract.SkillInstruction, bool) {
	content, errorValue := os.ReadFile(skillFile)
	if errorValue != nil {
		return agentcontract.SkillInstruction{}, false
	}
	frontMatter, body := splitFrontMatter(string(content))
	name := frontMatter["name"]
	if name == "" {
		name = filepath.Base(filepath.Dir(skillFile))
	}
	checksum := sha256.Sum256(content)
	return agentcontract.SkillInstruction{
		Name:        name,
		Description: frontMatter["description"],
		Prompt:      strings.TrimSpace(body),
		Source: agentcontract.InstructionSource{
			Path:      skillFile,
			SkillName: name,
			ByteSize:  len(content),
			SHA256:    hex.EncodeToString(checksum[:]),
		},
	}, true
}

func splitFrontMatter(content string) (map[string]string, string) {
	frontMatter := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return frontMatter, content
	}
	consumed := len(scanner.Text()) + 1
	for scanner.Scan() {
		consumed += len(scanner.Text()) + 1
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			return frontMatter, content[min(consumed, len(content)):]
		}
		key, value, isPair := strings.Cut(line, ":")
		if isPair {
			frontMatter[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return map[string]string{}, content
}
