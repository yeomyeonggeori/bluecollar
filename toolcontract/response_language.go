package toolcontract

import "strings"

const (
	ResponseLanguageKorean  = "ko"
	ResponseLanguageEnglish = "en"
	ResponseLanguageOther   = "other"
)

type ResponseLanguage struct {
	Code string
	Name string
}

var ResponseLanguages = []ResponseLanguage{
	{Code: ResponseLanguageEnglish, Name: "English"},
	{Code: ResponseLanguageKorean, Name: "Korean"},
	{Code: "ja", Name: "Japanese"},
	{Code: "zh", Name: "Chinese"},
	{Code: "es", Name: "Spanish"},
	{Code: "fr", Name: "French"},
	{Code: "de", Name: "German"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "it", Name: "Italian"},
	{Code: "ru", Name: "Russian"},
	{Code: "ar", Name: "Arabic"},
	{Code: "hi", Name: "Hindi"},
	{Code: "id", Name: "Indonesian"},
	{Code: "vi", Name: "Vietnamese"},
	{Code: "th", Name: "Thai"},
	{Code: "tr", Name: "Turkish"},
}

func ResolveResponseLanguage(values ...string) string {
	for _, value := range values {
		if normalizedValue := NormalizeResponseLanguage(value); normalizedValue != "" {
			return normalizedValue
		}
	}
	return ""
}

func NormalizeResponseLanguage(value string) string {
	trimmedValue := strings.TrimSpace(value)
	for _, language := range ResponseLanguages {
		if strings.EqualFold(trimmedValue, language.Code) || strings.EqualFold(trimmedValue, language.Name) {
			return language.Code
		}
	}
	return ""
}

func ResponseLanguageName(value string) string {
	code := NormalizeResponseLanguage(value)
	for _, language := range ResponseLanguages {
		if language.Code == code {
			return language.Name
		}
	}
	return ""
}
