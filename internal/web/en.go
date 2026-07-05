package web

// enTranslations holds the English strings for the server-rendered degraded
// (DB version mismatch) page and shared HTTP error responses — the only
// surfaces still rendered by the Go backend. All other UI text lives in the
// Vue SPA (webgui/src/locales).
var enTranslations = map[string]string{
	"title.db_mismatch":          "Database Version Mismatch",
	"error.database_unavailable": "database unavailable",
	"error.internal":             "internal server error",
}
