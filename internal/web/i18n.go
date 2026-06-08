package web

import (
	"net/http"
	"net/url"
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
	localeEnglish: {
		"language.label":             "Language",
		"language.english":           "English",
		"language.russian":           "Russian",
		"title.access_denied":        "Access denied",
		"title.selection":            "Route selection",
		"title.login":                "Sign in",
		"title.admin":                "Administration",
		"title.user":                 "User %s",
		"access_denied.heading":      "Access denied",
		"admin.heading":              "Administration",
		"admin.link":                 "Administration",
		"user_interface.link":        "User interface",
		"login.password":             "Password",
		"login.submit":               "Sign in",
		"login.invalid_password":     "Invalid password",
		"selection.heading":          "Service selection",
		"selection.category_hint":    "Selecting a whole category also includes services added to it later.",
		"selection.saved":            "Selection saved and BGP announcements updated.",
		"selection.save_failed":      "BGP announcements could not be updated.",
		"selection.locked":           "Selection is locked by the administrator.",
		"selection.selected":         "Selected:",
		"selection.apply_hint":       "Changes take effect immediately after saving",
		"selection.save":             "Save routes",
		"selection.whole_category":   "all",
		"selection.empty":            "The catalog is empty.",
		"filters.heading":            "Route filtering",
		"filters.explanation":        `The "extend" mode combines global and user lists. The "replace" mode uses only user lists. An empty allow list permits all selected routes; deny entries are subtracted from broader prefixes.`,
		"filters.mode":               "Filter mode",
		"filters.mode_global":        "use global lists only",
		"filters.mode_extend":        "extend global lists with user lists",
		"filters.mode_override":      "replace global lists with user lists",
		"filters.allow":              "Allow CIDR, one per line",
		"filters.allow_placeholder":  "Empty = allow all",
		"filters.deny":               "Deny CIDR, one per line",
		"filters.save":               "Save filter",
		"filters.managed_override":   "User lists managed by the administrator are used.",
		"filters.managed_extend":     "Global lists are extended with user lists managed by the administrator.",
		"filters.managed_global":     "Global lists managed by the administrator are used.",
		"feeds.heading":              "Feeds",
		"feeds.name":                 "Name",
		"feeds.last_download":        "Last download",
		"feeds.error":                "Error",
		"feeds.enabled":              "Enabled",
		"feeds.actions":              "Actions",
		"feeds.add":                  "Add feed",
		"feeds.download_now":         "Download feeds now",
		"feeds.delete_confirm":       "Delete this feed and its downloaded catalog entries?",
		"common.add":                 "Add",
		"common.save":                "Save",
		"common.delete":              "Delete",
		"global_filters.heading":     "Global route filtering",
		"global_filters.explanation": "An empty allow list permits all selected routes. Deny subnets are subtracted from broader announcements. Default routes from feeds are always discarded.",
		"global_filters.save":        "Save global filter",
		"users.heading":              "Users",
		"users.cidr":                 "CIDR",
		"users.peer":                 "BGP peer",
		"users.asn":                  "ASN",
		"users.status":               "Status",
		"users.add":                  "Add user",
		"users.networks":             "User CIDRs, comma-separated",
		"users.peer_ip":              "BGP peer IP",
		"users.peer_asn":             "Peer ASN",
		"users.next_hop":             "Announcement next hop",
		"users.bgp_password":         "BGP MD5 password",
		"users.allow_filter_editing": "allow the user to configure the filter mode and lists",
		"user.settings":              "User settings",
		"user.password_set":          "Password is set; leave empty to keep it",
		"user.password_not_set":      "Not set",
		"user.clear_password":        "clear BGP MD5 password",
		"user.enabled":               "user is enabled",
		"user.lock_selection":        "prevent the user from changing the selection",
		"user.save":                  "Save settings",
		"user.delete":                "Delete user",
		"user.delete_confirm":        "Delete this user? This also deletes their service selection.",
		"error.forbidden":            "forbidden",
		"error.selection_locked":     "selection is locked by the administrator",
		"error.filters_managed":      "route filters are managed by the administrator",
		"error.bad_request":          "bad request",
		"error.bad_user_id":          "bad user id",
		"error.bad_feed_id":          "bad feed id",
		"error.database_unavailable": "database unavailable",
		"error.internal":             "internal server error",
	},
	localeRussian: {
		"language.label":             "Язык",
		"language.english":           "Английский",
		"language.russian":           "Русский",
		"title.access_denied":        "Нет доступа",
		"title.selection":            "Выбор маршрутов",
		"title.login":                "Вход",
		"title.admin":                "Админка",
		"title.user":                 "Пользователь %s",
		"access_denied.heading":      "Нет доступа",
		"admin.heading":              "Админка",
		"admin.link":                 "Админка",
		"user_interface.link":        "Пользовательский интерфейс",
		"login.password":             "Пароль",
		"login.submit":               "Войти",
		"login.invalid_password":     "Неверный пароль",
		"selection.heading":          "Выбор сервисов",
		"selection.category_hint":    "Категория целиком включает также сервисы, которые появятся в ней позже.",
		"selection.saved":            "Выбор сохранён, BGP-анонсы обновлены.",
		"selection.save_failed":      "Не удалось обновить BGP-анонсы.",
		"selection.locked":           "Выбор заблокирован администратором.",
		"selection.selected":         "Выбрано:",
		"selection.apply_hint":       "Изменения применяются сразу после сохранения",
		"selection.save":             "Сохранить маршруты",
		"selection.whole_category":   "целиком",
		"selection.empty":            "Каталог пока пуст.",
		"filters.heading":            "Фильтрация маршрутов",
		"filters.explanation":        `Режим "дополнить" применяет глобальные и пользовательские списки вместе. Режим "заменить" использует только пользовательские списки. Пустой allow разрешает все выбранные маршруты; deny вырезается из широких префиксов.`,
		"filters.mode":               "Режим фильтрации",
		"filters.mode_global":        "использовать только глобальные списки",
		"filters.mode_extend":        "дополнить глобальные списки пользовательскими",
		"filters.mode_override":      "заменить глобальные списки пользовательскими",
		"filters.allow":              "Allow CIDR, по одному на строку",
		"filters.allow_placeholder":  "Пусто = разрешить всё",
		"filters.deny":               "Deny CIDR, по одному на строку",
		"filters.save":               "Сохранить фильтр",
		"filters.managed_override":   "Используются пользовательские списки, управляемые администратором.",
		"filters.managed_extend":     "Глобальные списки дополнены пользовательскими списками администратора.",
		"filters.managed_global":     "Используются глобальные списки администратора.",
		"feeds.heading":              "Фиды",
		"feeds.name":                 "Имя",
		"feeds.last_download":        "Последняя загрузка",
		"feeds.error":                "Ошибка",
		"feeds.enabled":              "Включён",
		"feeds.actions":              "Действия",
		"feeds.add":                  "Добавить фид",
		"feeds.download_now":         "Скачать фиды сейчас",
		"feeds.delete_confirm":       "Удалить фид и загруженные из него записи каталога?",
		"common.add":                 "Добавить",
		"common.save":                "Сохранить",
		"common.delete":              "Удалить",
		"global_filters.heading":     "Глобальная фильтрация маршрутов",
		"global_filters.explanation": "Пустой allow разрешает все выбранные маршруты. Deny-подсети физически вырезаются из более широких анонсов. Default routes из фидов всегда отбрасываются.",
		"global_filters.save":        "Сохранить глобальный фильтр",
		"users.heading":              "Пользователи",
		"users.cidr":                 "CIDR",
		"users.peer":                 "BGP peer",
		"users.asn":                  "ASN",
		"users.status":               "Состояние",
		"users.add":                  "Добавить пользователя",
		"users.networks":             "Пользовательские CIDR, через запятую",
		"users.peer_ip":              "IP BGP peer",
		"users.peer_asn":             "ASN peer",
		"users.next_hop":             "Next hop для анонсов",
		"users.bgp_password":         "BGP MD5 пароль",
		"users.allow_filter_editing": "разрешить пользователю настраивать режим и списки фильтрации",
		"user.settings":              "Параметры пользователя",
		"user.password_set":          "Пароль задан; пустое поле не изменит его",
		"user.password_not_set":      "Не задан",
		"user.clear_password":        "очистить BGP MD5 пароль",
		"user.enabled":               "пользователь включён",
		"user.lock_selection":        "запретить пользователю менять выбор",
		"user.save":                  "Сохранить параметры",
		"user.delete":                "Удалить пользователя",
		"user.delete_confirm":        "Удалить пользователя? Это также удалит его выбор сервисов.",
		"error.forbidden":            "нет доступа",
		"error.selection_locked":     "выбор заблокирован администратором",
		"error.filters_managed":      "фильтрация маршрутов управляется администратором",
		"error.bad_request":          "неверный запрос",
		"error.bad_user_id":          "неверный идентификатор пользователя",
		"error.bad_feed_id":          "неверный идентификатор фида",
		"error.database_unavailable": "база данных недоступна",
		"error.internal":             "внутренняя ошибка сервера",
	},
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

func languageURL(r *http.Request, lang locale) string {
	query := cloneQuery(r.URL.Query())
	query.Set("lang", string(lang))
	path := r.URL.Path
	if queryString := query.Encode(); queryString != "" {
		path += "?" + queryString
	}
	return path
}

func cloneQuery(source url.Values) url.Values {
	result := make(url.Values, len(source))
	for key, values := range source {
		result[key] = append([]string(nil), values...)
	}
	return result
}
