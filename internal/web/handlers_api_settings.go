package web

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// settingFieldJSON is the JSON representation of a settings field.
type settingFieldJSON struct {
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Options      map[string]string `json:"options,omitempty"`
	Value        string            `json:"value"`
	EnvOverride  bool              `json:"env_override"`
	EnvVar       string            `json:"env_var"`
	Restart      bool              `json:"restart"`
	Hint         string            `json:"hint,omitempty"`
	DefaultValue string            `json:"default_value,omitempty"`
	Constraint   string            `json:"constraint,omitempty"`
}

// settingSectionJSON is the JSON representation of a settings section.
type settingSectionJSON struct {
	Name   string             `json:"name"`
	Fields []settingFieldJSON `json:"fields"`
}

// apiSettingsGet handles GET /api/admin/settings.
func (s *Server) apiSettingsGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbSettings, _ := s.store.GetAllSettings(ctx) //nolint:errcheck // best-effort
	all := allSettings()

	sectionMap := make(map[string][]settingFieldJSON)
	sectionOrder := make([]string, 0)

	for _, f := range all {
		fj := settingFieldJSON{
			Key:     f.Key,
			Name:    f.Name,
			Type:    f.Type,
			Options: f.Options,
			EnvVar:  f.EnvVar,
			Restart: f.Restart,
			Hint:    f.Name + "_hint",
		}

		// Constraints
		switch f.Type {
		case "number":
			fj.Constraint = "settings.constraint_number"
		case "text":
			fj.Constraint = "settings.constraint_text"
		case "select":
			fj.Constraint = "settings.constraint_select"
		case "bool":
			fj.Constraint = "settings.constraint_bool"
		}

		// Populate value
		if v := os.Getenv(f.EnvVar); v != "" {
			fj.Value = v
			fj.EnvOverride = true
		} else if v, ok := dbSettings[f.Key]; ok {
			fj.Value = v
		} else {
			fj.DefaultValue = configDefaultValue(s.cfg, f.Key)
		}

		if _, ok := sectionMap[f.Section]; !ok {
			sectionOrder = append(sectionOrder, f.Section)
		}
		sectionMap[f.Section] = append(sectionMap[f.Section], fj)
	}

	sections := make([]settingSectionJSON, 0, len(sectionOrder))
	for _, sKey := range sectionOrder {
		sections = append(sections, settingSectionJSON{
			Name:   sKey,
			Fields: sectionMap[sKey],
		})
	}

	// Add global route filters section
	globalFilters, _ := s.store.GlobalRouteFilters(ctx) //nolint:errcheck // best-effort
	allowVal := strings.Join(globalFilters.Allow, "\n")
	denyVal := strings.Join(globalFilters.Deny, "\n")

	filtersSection := settingSectionJSON{
		Name: "settings.section_filters",
		Fields: []settingFieldJSON{
			{
				Key:   "filter_allow",
				Name:  "settings.filter_allow",
				Type:  "text",
				Hint:  "settings.filter_allow_hint",
				Value: allowVal,
			},
			{
				Key:   "filter_deny",
				Name:  "settings.filter_deny",
				Type:  "text",
				Hint:  "settings.filter_deny_hint",
				Value: denyVal,
			},
		},
	}
	sections = append(sections, filtersSection)

	writeJSON(w, http.StatusOK, map[string]any{
		"sections": sections,
	})
}

// apiSettingsPut handles PUT /api/admin/settings.
func (s *Server) apiSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body map[string]*string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	// Delete nil-valued keys from DB (use default / clear override).
	for key, valPtr := range body {
		if valPtr == nil {
			if err := s.store.DeleteSetting(r.Context(), key); err != nil {
				writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to reset setting: " + key})
				return
			}
		}
	}

	settings := make(map[string]string)
	knownKeys := allSettingKeys()

	for _, key := range knownKeys {
		f := fieldByKey(key)
		if f == nil {
			continue
		}
		// Skip env-overridden fields
		if isEnvOverridden(f.EnvVar) {
			continue
		}

		if valPtr, ok := body[key]; ok {
			if valPtr == nil {
				// Use default — already deleted above, skip from settings map.
				continue
			}
			val := *valPtr
			// Basic validation
			if f.Type == "number" {
				if _, err := strconv.ParseInt(val, 10, 64); err != nil && val != "" {
					writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid number for " + key})
					return
				}
			}
			if f.Type == "bool" {
				if val == "true" || val == "on" || val == "1" {
					settings[key] = "true"
				} else {
					settings[key] = "false"
				}
			} else {
				settings[key] = val
			}
		}
	}

	if err := s.store.SaveSettings(r.Context(), settings); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to save settings"})
		return
	}

	s.reloadRuntimeSettings(r.Context())

	// Also save global route filters if provided
	if allowPtr, ok := body["filter_allow"]; ok {
		if denyPtr, ok2 := body["filter_deny"]; ok2 {
			allow := ""
			deny := ""
			if allowPtr != nil {
				allow = *allowPtr
			}
			if denyPtr != nil {
				deny = *denyPtr
			}
			filters := store.RouteFilters{
				Allow: splitCIDRs(allow),
				Deny:  splitCIDRs(deny),
			}
			if normalized, err := store.NormalizeRouteFilters(filters); err == nil {
				_ = s.store.SetGlobalRouteFilters(r.Context(), normalized) //nolint:errcheck // best-effort filter update in settings save
				if s.bgp != nil {
					if err := s.bgp.Reconcile(r.Context()); err != nil {
						logging.FromContext(r.Context()).Debug("bgp reconcile failed after global filter change", "error", err)
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiSettingsPurgeMetrics handles POST /api/admin/settings/purge-metrics
func (s *Server) apiSettingsPurgeMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := s.store.DB.ExecContext(ctx, "DELETE FROM user_snapshots"); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if _, err := s.store.DB.ExecContext(ctx, "DELETE FROM feed_snapshots"); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}
