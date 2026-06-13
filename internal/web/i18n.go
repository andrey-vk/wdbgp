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
		"title.login":                "Login",
		"title.admin":                "Administration",
		"title.user":                 "User %s",
		"access_denied.heading":      "Access denied",
		"admin.heading":              "Administration",
		"admin.link":                 "Administration",
		"bgp.established":            "BGP session established",
		"bgp.unknown":                "BGP state unknown",
		"debug.heading":              "CIDR diagnostics",
		"debug.description":          "Inspect which enabled catalog services and user selections cover an IP address or subnet. User coverage is shown before and after effective route allow/deny filters.",
		"debug.input":                "IP address or CIDR",
		"debug.submit":               "Analyze",
		"debug.results":              "Coverage diagnostics",
		"debug.full_services":        "Services with full coverage",
		"debug.partial_services":     "Services with partial coverage",
		"debug.combined":             "Combined service coverage",
		"debug.users":                "Enabled users with matching selections",
		"debug.no_matches":           "No matching enabled services or users.",
		"debug.no_items":             "None",
		"debug.coverage":             "coverage",
		"debug.before_filters":       "before filters",
		"debug.after_filters":        "after filters",
		"debug.close":                "Close",
		"debug.request_failed":       "Could not analyze this CIDR.",
		"catalog.mode":               "Catalog mode",
		"catalog.modes":              "Catalog modes",
		"catalog.modes_hint":         "Only enabled modes can be selected by users or used for announcements and diagnostics.",
		"catalog.key":                "Key",
		"catalog.category":           "Category",
		"catalog.service":            "Service",
		"catalog.disabled":           "disabled",
		"catalog.managed":            "The catalog mode is managed by the administrator.",
		"user_interface.link":        "User interface",
		"login.password":             "Password",
		"login.submit":               "Sign in",
		"login.invalid_password":     "Invalid password",
		"login.rate_limit":           "Too many login attempts. Please try again later.",
		"login.error_empty":          "Login and password are required.",
		"login.error_invalid":        "Invalid login or password.",
		"login.error_ip":             "Your IP address does not match the user's authorized networks.",
		"login.not_implemented":      "Login with credentials is not yet implemented. Please use network auth for now.",
		"user.login":                 "Login",
		"user.password":              "Password",
		"admin.logout":               "Logout",
		"selection.heading":          "Service selection",
		"selection.category_hint":    "Selecting a whole category also includes services added to it later.",
		"selection.saved":            "Selection saved and BGP announcements updated.",
		"selection.save_failed":      "BGP announcements could not be updated.",
		"selection.locked":           "Selection is locked by the administrator.",
		"selection.selected":         "Selected:",
		"selection.category_one":     "category",
		"selection.category_few":     "categories",
		"selection.category_many":    "categories",
		"selection.service_one":      "service",
		"selection.service_few":      "services",
		"selection.service_many":     "services",
		"selection.in_categories":    "in them",
		"selection.standalone_one":   "standalone service",
		"selection.standalone_few":   "standalone services",
		"selection.standalone_many":  "standalone services",
		"selection.apply_hint":       "Changes take effect immediately after saving",
		"selection.save":             "Save routes",
		"selection.whole_category":   "all",
		"selection.empty":            "The catalog is empty.",
		"selection.prefixes":         "Prefixes",
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
		"feeds.last_sync":            "Last sync",
		"feeds.status":               "Status",
		"feeds.error":                "Error",
		"feeds.enabled":              "Enabled",
		"feeds.actions":              "Actions",
		"feeds.add":                  "Add feed",
		"feeds.download_now":         "Download feeds now",
		"feeds.edit":                 "Edit Feed",
		"feeds.url":                  "URL",
		"feeds.adapter":              "Adapter",
		"feeds.sync_interval":        "Sync interval (seconds)",
		"feeds.default_interval":     "Default",
		"feeds.delete_confirm":       "Delete this feed and its downloaded catalog entries?",
		"adapters.heading":           "Feed adapters",
		"adapters.adapter":           "Adapter",
		"adapters.key":               "Key",
		"adapters.add":               "Add adapter",
		"adapters.source":            "JavaScript source",
		"adapters.allowed_hosts":     "Additional allowed HTTP hosts",
		"adapters.hint":              "Adapters run with a limited API. Saving validates JavaScript syntax; changes take effect on the next synchronization.",
		"adapters.test":              "Test",
		"adapters.test_feed":         "Test feed",
		"adapters.select_feed":       "Select a feed",
		"adapters.feed":              "Feed",
		"adapters.test_result":       "Adapter test result",
		"adapters.test_success":      "Adapter returned %d normalized CIDRs.",
		"adapters.preview_truncated": "Showing the first 100 CIDRs.",
		"adapters.preview_empty":     "The adapter returned no CIDRs.",
		"adapters.edit":              "Edit",
		"adapters.revision":          "Revision",
		"adapters.built_in":          "built in",
		"adapters.error":             "JavaScript error",
		"adapters.back_to_editor":    "Back to adapter",
		"adapters.reset":             "Reset to distribution version",
		"adapters.reset_confirm":     "Replace this adapter with the distribution version?",
		"title.adapter_test":         "Adapter test",
		"title.adapter_edit":         "Edit adapter",
		"title.communities":          "Communities",
		"communities.title":          "Communities",
		"communities.link":           "Communities",
		"communities.group_community":  "Group community",
		"communities.service_community": "Service community",
		"communities.auto_generated":    "Auto-generated",
		"communities.revert":            "Revert to auto",
		"communities.apply":             "Apply Changes",
		"communities.cancel":            "Cancel",
		"communities.confirm":           "N communities will be updated. Continue?",
		"communities.saved":             "Communities saved.",
		"communities.reset_all":         "Reset all to auto",
		"communities.auto_generate":     "Auto-generate missing",
		"communities.no_changes":        "No changes",
		"communities.generate_confirm": "Generate community numbers for all categories and services that do not have one yet?",
		"communities.reset_confirm":    "This will reset ALL community numbers to auto-generated values. Any manual changes will be lost. Continue?",
		"communities.apply_edit_confirm": "Apply changes to this community number?",
		"communities.duplicate_error":  "This community number is already used. Please choose a different number.",
		"communities.changed_one":      "community number changed",
		"communities.changed_many":     "community numbers changed",
		"communities.reverted":         "reverted to auto",
		"common.add":                 "Add",
		"common.edit":                "Edit",
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
		"users.web_auth":              "Web auth",
		"users.web_auth_network":      "Network (IP only)",
		"users.web_auth_login":        "Login (credentials)",
		"users.web_auth_both":         "Both (IP + credentials)",
		"users.web_auth_hint":         "How the user is identified when accessing the selection page. Separate from BGP peer authentication.",
		"users.credentials":           "Credentials",
		"users.new_credential":        "New login",
		"users.allow_filter_editing": "allow the user to configure the filter mode and lists",
		"users.allow_mode_editing":   "allow the user to change the catalog mode",
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
		"error.catalog_managed":      "catalog mode is managed by the administrator",
		"error.bad_request":          "bad request",
		"error.bad_user_id":          "bad user id",
		"error.bad_feed_id":          "bad feed id",
		"error.bad_mode_id":          "bad catalog mode id",
		"error.database_unavailable": "database unavailable",
		"error.internal":             "internal server error",
		"nav.dashboard":              "Dashboard",
		"nav.users":                  "Users",
		"nav.feeds":                  "Feeds",
		"nav.communities":            "Communities",
		"nav.adapters":               "Adapters",
		"nav.settings":               "Settings",
		"nav.user_page":              "User page",
		"stats.prefixes":             "Prefixes announced",
		"stats.users":                "Users",
		"stats.bgp_peers":            "BGP peers",
		"stats.feeds":                "Feeds enabled",
		"stats.categories":           "Categories",
		"stats.services":             "Services",
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
		"bgp.established":            "BGP-сессия установлена",
		"bgp.unknown":                "Состояние BGP неизвестно",
		"debug.heading":              "Диагностика CIDR",
		"debug.description":          "Показывает, какие сервисы включённых фидов и выборы пользователей покрывают IP-адрес или подсеть. Покрытие пользователей отображается до и после применения эффективных allow/deny-фильтров маршрутов.",
		"debug.input":                "IP-адрес или CIDR",
		"debug.submit":               "Проверить",
		"debug.results":              "Диагностика покрытия",
		"debug.full_services":        "Сервисы с полным покрытием",
		"debug.partial_services":     "Сервисы с частичным покрытием",
		"debug.combined":             "Совокупное покрытие сервисами",
		"debug.users":                "Включённые пользователи с подходящим выбором",
		"debug.no_matches":           "Подходящие включённые сервисы и пользователи не найдены.",
		"debug.no_items":             "Нет",
		"debug.coverage":             "покрытие",
		"debug.before_filters":       "до фильтров",
		"debug.after_filters":        "после фильтров",
		"debug.close":                "Закрыть",
		"debug.request_failed":       "Не удалось выполнить анализ CIDR.",
		"catalog.mode":               "Режим каталога",
		"catalog.modes":              "Режимы каталогов",
		"catalog.modes_hint":         "Только включённые режимы доступны пользователям и участвуют в анонсах и диагностике.",
		"catalog.key":                "Ключ",
		"catalog.category":           "Категория",
		"catalog.service":            "Сервис",
		"catalog.disabled":           "отключён",
		"catalog.managed":            "Режим каталога управляется администратором.",
		"user_interface.link":        "Пользовательский интерфейс",
		"login.password":             "Пароль",
		"login.submit":               "Войти",
		"login.invalid_password":     "Неверный пароль",
		"login.rate_limit":           "Слишком много попыток входа. Попробуйте позже.",
		"login.error_empty":          "Логин и пароль обязательны.",
		"login.error_invalid":        "Неверный логин или пароль.",
		"login.error_ip":             "Ваш IP-адрес не совпадает с разрешёнными сетями пользователя.",
		"login.not_implemented":      "Вход по учетным данным еще не реализован. Пожалуйста, используйте сетевую аутентификацию.",
		"user.login":                 "Логин",
		"user.password":              "Пароль",
		"admin.logout":               "Выйти",
		"selection.heading":          "Выбор сервисов",
		"selection.category_hint":    "Категория целиком включает также сервисы, которые появятся в ней позже.",
		"selection.saved":            "Выбор сохранён, BGP-анонсы обновлены.",
		"selection.save_failed":      "Не удалось обновить BGP-анонсы.",
		"selection.locked":           "Выбор заблокирован администратором.",
		"selection.selected":         "Выбрано:",
		"selection.category_one":     "категория",
		"selection.category_few":     "категории",
		"selection.category_many":    "категорий",
		"selection.service_one":      "сервис",
		"selection.service_few":      "сервиса",
		"selection.service_many":     "сервисов",
		"selection.in_categories":    "в них",
		"selection.standalone_one":   "отдельный сервис",
		"selection.standalone_few":   "отдельных сервиса",
		"selection.standalone_many":  "отдельных сервисов",
		"selection.apply_hint":       "Изменения применяются сразу после сохранения",
		"selection.save":             "Сохранить маршруты",
		"selection.whole_category":   "целиком",
		"selection.empty":            "Каталог пока пуст.",
		"selection.prefixes":         "Префиксов",
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
		"feeds.last_sync":            "Последняя синхронизация",
		"feeds.status":               "Состояние",
		"feeds.error":                "Ошибка",
		"feeds.enabled":              "Включён",
		"feeds.actions":              "Действия",
		"feeds.add":                  "Добавить фид",
		"feeds.download_now":         "Скачать фиды сейчас",
		"feeds.edit":                 "Редактировать фид",
		"feeds.url":                  "URL",
		"feeds.adapter":              "Адаптер",
		"feeds.sync_interval":        "Интервал синхронизации (секунд)",
		"feeds.default_interval":     "По умолчанию",
		"feeds.delete_confirm":       "Удалить фид и загруженные из него записи каталога?",
		"adapters.heading":           "Адаптеры фидов",
		"adapters.adapter":           "Адаптер",
		"adapters.key":               "Ключ",
		"adapters.add":               "Добавить адаптер",
		"adapters.source":            "Исходный код JavaScript",
		"adapters.allowed_hosts":     "Дополнительные разрешённые HTTP-хосты",
		"adapters.hint":              "Адаптеры работают с ограниченным API. При сохранении проверяется синтаксис JavaScript; изменения применяются при следующей синхронизации.",
		"adapters.test":              "Тест",
		"adapters.test_feed":         "Фид для теста",
		"adapters.select_feed":       "Выберите фид",
		"adapters.feed":              "Фид",
		"adapters.test_result":       "Результат теста адаптера",
		"adapters.test_success":      "Адаптер вернул %d нормализованных CIDR.",
		"adapters.preview_truncated": "Показаны первые 100 CIDR.",
		"adapters.preview_empty":     "Адаптер не вернул CIDR.",
		"adapters.edit":              "Редактировать",
		"adapters.revision":          "Ревизия",
		"adapters.built_in":          "встроенный",
		"adapters.error":             "Ошибка JavaScript",
		"adapters.back_to_editor":    "Вернуться к адаптеру",
		"adapters.reset":             "Сбросить к версии из дистрибутива",
		"adapters.reset_confirm":     "Заменить адаптер версией из дистрибутива?",
		"title.adapter_test":         "Тест адаптера",
		"title.adapter_edit":         "Редактирование адаптера",
		"title.communities":          "Комьюнити",
		"communities.title":          "Комьюнити",
		"communities.link":           "Комьюнити",
		"communities.group_community":  "Комьюнити группы",
		"communities.service_community": "Комьюнити сервиса",
		"communities.auto_generated":    "Авто-сгенерировано",
		"communities.revert":            "Вернуть авто",
		"communities.apply":             "Применить изменения",
		"communities.cancel":            "Отмена",
		"communities.confirm":           "Будет обновлено N комьюнити. Продолжить?",
		"communities.saved":             "Комьюнити сохранены.",
		"communities.reset_all":         "Сбросить все на авто",
		"communities.auto_generate":     "Авто-генерация отсутствующих",
		"communities.no_changes":        "Нет изменений",
		"communities.generate_confirm": "Сгенерировать номера комьюнити для всех категорий и сервисов, у которых их нет?",
		"communities.reset_confirm":    "Это сбросит ВСЕ номера комьюнити на авто-сгенерированные значения. Ручные изменения будут потеряны. Продолжить?",
		"communities.apply_edit_confirm": "Применить изменения для этого номера комьюнити?",
		"communities.duplicate_error":  "Этот номер комьюнити уже используется. Выберите другой номер.",
		"communities.changed_one":      "номер комьюнити изменён",
		"communities.changed_many":     "номеров комьюнити изменено",
		"communities.reverted":         "возвращено к авто",
		"common.add":                 "Добавить",
		"common.edit":                "Редактировать",
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
		"users.web_auth":              "Веб-аутентификация",
		"users.web_auth_network":      "Сеть (только IP)",
		"users.web_auth_login":        "Логин (учетные данные)",
		"users.web_auth_both":         "Оба (IP + учетные данные)",
		"users.web_auth_hint":         "Как пользователь идентифицируется при доступе к странице выбора. Отдельно от аутентификации BGP-пира.",
		"users.credentials":           "Учётные данные",
		"users.new_credential":        "Новый логин",
		"users.allow_filter_editing": "разрешить пользователю настраивать режим и списки фильтрации",
		"users.allow_mode_editing":   "разрешить пользователю менять режим каталога",
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
		"error.catalog_managed":      "режим каталога управляется администратором",
		"error.bad_request":          "неверный запрос",
		"error.bad_user_id":          "неверный идентификатор пользователя",
		"error.bad_feed_id":          "неверный идентификатор фида",
		"error.bad_mode_id":          "неверный идентификатор режима каталога",
		"error.database_unavailable": "база данных недоступна",
		"error.internal":             "внутренняя ошибка сервера",
		"nav.dashboard":              "Дашборд",
		"nav.users":                  "Пользователи",
		"nav.feeds":                  "Фиды",
		"nav.communities":            "Комьюнити",
		"nav.adapters":               "Адаптеры",
		"nav.settings":               "Настройки",
		"nav.user_page":              "Страница пользователя",
		"stats.prefixes":             "Анонсировано префиксов",
		"stats.users":                "Пользователей",
		"stats.bgp_peers":            "BGP-пиров",
		"stats.feeds":                "Фидов активно",
		"stats.categories":           "Категорий",
		"stats.services":             "Сервисов",
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

func pluralTranslation(lang locale, count int, oneKey, fewKey, manyKey string) string {
	key := manyKey
	if lang == localeRussian {
		mod10 := count % 10
		mod100 := count % 100
		if mod10 == 1 && mod100 != 11 {
			key = oneKey
		} else if mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14) {
			key = fewKey
		}
	} else if count == 1 {
		key = oneKey
	}
	return translate(lang, key)
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
