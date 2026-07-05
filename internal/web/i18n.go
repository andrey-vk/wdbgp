package web

import (
	"net/http"
	"strings"

	"golang.org/x/text/language"
)

type locale string

const (
	localeEnglish locale = "en"
	localeRussian locale = "ru"
)

const languageCookieName = "wdbgp_language"

var supportedLanguages = []language.Tag{
	language.English,
	language.Russian,
}

var languageMatcher = language.NewMatcher(supportedLanguages)

var translations = map[locale]map[string]string{
	localeEnglish: enTranslations,
	localeRussian: ruTranslations,
}

func translate(lang locale, key string) string {
	if message := translations[lang][key]; message != "" {
		return message
	}
	if message := translations[localeEnglish][key]; message != "" {
		return message
	}
	return key
}

func requestLocale(r *http.Request, fallback locale) (locale, bool) {
	if value := r.URL.Query().Get("lang"); value != "" {
		if lang, ok := parseLocale(value); ok {
			return lang, true
		}
	}
	if cookie, err := r.Cookie(languageCookieName); err == nil {
		if lang, ok := parseLocale(cookie.Value); ok {
			return lang, false
		}
	}
	header := r.Header.Get("Accept-Language")
	if header == "" {
		return fallback, false
	}
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return fallback, false
	}
	tag, _, confidence := languageMatcher.Match(tags...)
	if confidence == language.No {
		return fallback, false
	}
	if base, _ := tag.Base(); base.String() == "en" {
		return localeEnglish, false
	}
	return localeRussian, false
}

func parseLocale(value string) (locale, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en":
		return localeEnglish, true
	case "ru":
		return localeRussian, true
	default:
		return "", false
	}
}
