package web

// ruTranslations holds the Russian strings for the server-rendered degraded
// (DB version mismatch) page and shared HTTP error responses — the only
// surfaces still rendered by the Go backend. All other UI text lives in the
// Vue SPA (webgui/src/locales).
var ruTranslations = map[string]string{
	"title.db_mismatch":          "Несоответствие версии базы данных",
	"error.database_unavailable": "база данных недоступна",
	"error.internal":             "внутренняя ошибка сервера",
}
