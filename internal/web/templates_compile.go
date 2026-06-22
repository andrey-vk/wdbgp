package web

import (
	"fmt"
	"html/template"
	"strings"
)

func compileTemplates() map[locale]map[string]*template.Template {
	bodies := map[string]string{
		"access-denied": accessDeniedTemplate,
		"login":         loginTemplate,
		"selection":     selectionTemplate,
		"user-login":    userLoginTemplate,
		"degraded":      degradedTemplate,
	}
	// Fragment templates (body only, no pageStart/pageEnd) for htmx and shell embedding
	fragments := map[string]string{
		"debug":         debugTemplate,
		"dashboard":     dashboardTemplate,
		"communities":   communitiesTemplate,
		"adapter-edit":  adapterEditTemplate,
		"adapter-test":  adapterTestTemplate,
		"user-edit":     userEditTemplate,
		"users-list":    usersListTemplate,
		"feeds-list":    feedsListTemplate,
		"feed-edit":     feedEditTemplate,
		"adapters-list": adaptersListTemplate,
		"settings":      settingsTemplate,
		"modes":         modesTemplate,
		"mode-edit":     modeEditTemplate,
	}
	result := make(map[locale]map[string]*template.Template, len(translations))
	for lang := range translations {
		result[lang] = make(map[string]*template.Template, len(bodies)+len(fragments)+1)
		funcs := template.FuncMap{
			"join": strings.Join,
			"dict": func(values ...any) (map[string]any, error) {
				if len(values)%2 != 0 {
					return nil, fmt.Errorf("dict requires even number of arguments")
				}
				m := make(map[string]any, len(values)/2)
				for i := 0; i < len(values); i += 2 {
					key, ok := values[i].(string)
					if !ok {
						return nil, fmt.Errorf("dict keys must be strings")
					}
					m[key] = values[i+1]
				}
				return m, nil
			},
			"state": func(states map[string]string, peer string) string {
				if value := states[peer]; value != "" {
					return value
				}
				return "UNKNOWN"
			},
			"tr": func(key string) string {
				return translate(lang, key)
			},
			"plural": func(count int, oneKey, fewKey, manyKey string) string {
				return pluralTranslation(lang, count, oneKey, fewKey, manyKey)
			},
		}
		for name, body := range bodies {
			result[lang][name] = template.Must(template.New("page").Funcs(funcs).
				Parse(pageStart + body + pageEnd))
		}
		// Fragment templates for direct htmx rendering
		for name, body := range fragments {
			result[lang][name] = template.Must(template.New(name).Funcs(funcs).
				Parse(body))
		}
		// Admin shell (standalone layout)
		result[lang]["admin-shell"] = template.Must(template.New("admin-shell").Funcs(funcs).
			Parse(adminShellTemplate))
	}
	return result
}
