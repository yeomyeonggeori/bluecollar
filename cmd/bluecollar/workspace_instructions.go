package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const workspaceSkillsDirectory = ".agents/skills"

type workspaceInstructions struct {
	prompt  string
	skills  []agentcontract.SkillInstruction
	sources []agentcontract.InstructionSource
}

func loadWorkspaceInstructions(workspacePath string) workspaceInstructions {
	absoluteWorkspace, errorValue := filepath.Abs(strings.TrimSpace(workspacePath))
	if errorValue != nil || absoluteWorkspace == "" {
		return workspaceInstructions{}
	}
	instructions := workspaceInstructions{}
	instructions.prompt, instructions.sources = loadProjectInstructionFiles(absoluteWorkspace)
	skills, skillSources := loadCombinedSkills(absoluteWorkspace)
	instructions.skills = skills
	instructions.sources = append(instructions.sources, skillSources...)
	return instructions
}

func loadCombinedSkills(workspacePath string) ([]agentcontract.SkillInstruction, []agentcontract.InstructionSource) {
	byName := map[string]agentcontract.SkillInstruction{}
	order := []string{}
	add := func(skill agentcontract.SkillInstruction) {
		if _, exists := byName[skill.Name]; !exists {
			order = append(order, skill.Name)
		}
		byName[skill.Name] = skill
	}
	if home, errorValue := os.UserHomeDir(); errorValue == nil && strings.TrimSpace(home) != "" && home != workspacePath {
		globalSkills, _ := loadWorkspaceSkills(home)
		for _, skill := range globalSkills {
			add(skill)
		}
	}
	localSkills, _ := loadWorkspaceSkills(workspacePath)
	for _, skill := range localSkills {
		add(skill)
	}
	if _, hasGeminiSkill := byName[geminiCLIEndpointName]; !hasGeminiSkill && geminiCLIBinaryAvailable() {
		add(builtInGeminiCLISkill())
	}
	sort.Strings(order)
	skills := make([]agentcontract.SkillInstruction, 0, len(order))
	sources := make([]agentcontract.InstructionSource, 0, len(order))
	for _, name := range order {
		skill := byName[name]
		skills = append(skills, skill)
		sources = append(sources, skill.Source)
	}
	return skills, sources
}

func builtInGeminiCLISkill() agentcontract.SkillInstruction {
	body := strings.TrimSpace(`The gemini-api CLI is installed and carries its own authentication. Always pass --no-interactive when calling it through bash.

- Image generation/editing: gemini-api image --no-interactive "<prompt>" --aspect-ratio 16:9 --image-size 2K --out out.png
- Multimodal analysis (images, PDFs, audio, video, YouTube): gemini-api analyze --no-interactive -i <path-or-url> "<question>"
- Audio/video transcription: gemini-api transcribe --no-interactive -i <file> --format md --out transcript.md
- Text to speech: gemini-api tts --no-interactive "<text>" --voice Kore --out speech.wav
- Video generation: gemini-api video --no-interactive "<prompt>" --aspect-ratio 16:9 --resolution 1080p --out video.mp4
- Music generation: gemini-api music --no-interactive "<prompt>" --out music.wav
- Large media (>14 MB): upload with gemini-api files upload --no-interactive <file> --output-format json --jq .name and pass files/<id> to -i.`)
	checksum := sha256.Sum256([]byte(body))
	return agentcontract.SkillInstruction{
		Name:        geminiCLIEndpointName,
		Description: "Use the installed gemini-api CLI for multimodal generation and analysis (images, video, music, TTS, transcription, PDF/media understanding).",
		Prompt:      body,
		Source: agentcontract.InstructionSource{
			Path:      "builtin:gemini-api",
			SkillName: geminiCLIEndpointName,
			ByteSize:  len(body),
			SHA256:    hex.EncodeToString(checksum[:]),
		},
	}
}

func loadProjectInstructionFiles(workspacePath string) (string, []agentcontract.InstructionSource) {
	directories := instructionSearchDirectories(workspacePath)
	sections := []string{}
	sources := []agentcontract.InstructionSource{}
	for _, directory := range directories {
		path, content, found := readDirectoryInstructionFile(directory)
		if !found {
			continue
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue
		}
		checksum := sha256.Sum256(content)
		sections = append(sections, "Instructions from "+path+":\n"+trimmed)
		sources = append(sources, agentcontract.InstructionSource{
			Path:     path,
			ByteSize: len(content),
			SHA256:   hex.EncodeToString(checksum[:]),
		})
	}
	return strings.Join(sections, "\n\n"), sources
}

func instructionSearchDirectories(workspacePath string) []string {
	directories := []string{}
	current := workspacePath
	for {
		directories = append(directories, current)
		if directoryHasGitRoot(current) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			return []string{workspacePath}
		}
		current = parent
	}
	slices.Reverse(directories)
	return directories
}

func directoryHasGitRoot(directory string) bool {
	_, errorValue := os.Stat(filepath.Join(directory, ".git"))
	return errorValue == nil
}

func readDirectoryInstructionFile(directory string) (string, []byte, bool) {
	for _, filename := range []string{"AGENTS.md", "CLAUDE.md"} {
		candidate := filepath.Join(directory, filename)
		content, errorValue := os.ReadFile(candidate)
		if errorValue == nil {
			return candidate, content, true
		}
	}
	return "", nil, false
}

func loadWorkspaceSkills(workspacePath string) ([]agentcontract.SkillInstruction, []agentcontract.InstructionSource) {
	pattern := filepath.Join(workspacePath, workspaceSkillsDirectory, "*", "SKILL.md")
	skillFiles, _ := filepath.Glob(pattern)
	sort.Strings(skillFiles)
	skills := []agentcontract.SkillInstruction{}
	sources := []agentcontract.InstructionSource{}
	for _, skillFile := range skillFiles {
		skill, ok := parseSkillFile(skillFile)
		if !ok {
			continue
		}
		skills = append(skills, skill)
		sources = append(sources, skill.Source)
	}
	return skills, sources
}

func parseSkillFile(skillFile string) (agentcontract.SkillInstruction, bool) {
	content, errorValue := os.ReadFile(skillFile)
	if errorValue != nil {
		return agentcontract.SkillInstruction{}, false
	}
	frontMatter, body := splitSkillFrontMatter(string(content))
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

func splitSkillFrontMatter(content string) (map[string]string, string) {
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
