package agentcontract

import "github.com/yeomyeonggeori/bluecollar/toolcontract"

func ResponseLanguageInstruction(responseLanguage string) string {
	return "Write every user-facing reply, approval question, and recovery message in " + responseLanguagePhrase(responseLanguage) + ". Do not put emoji in message text unless the user explicitly asks for emoji; use message reactions for lightweight acknowledgement."
}

func responseLanguagePhrase(responseLanguage string) string {
	if languageName := toolcontract.ResponseLanguageName(responseLanguage); languageName != "" {
		return languageName
	}
	return "the language the requester wrote in"
}
