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



// apiSettingsGet handles GET /api/admin/settings.
func (s *Server) apiSettingsGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sj := s.settings.JSON()

	// Add global route filters (not in SettingsJSON, stored separately)
	globalFilters, _ := s.store.GlobalRouteFilters(ctx) //nolint:errcheck // best-effort
	filterAllow := strings.Join(globalFilters.Allow, "\n")
	filterDeny := strings.Join(globalFilters.Deny, "\n")

	resp := map[string]any{
		"settings": sj,
		"route_filters": map[string]any{
			"filter_allow": filterAllow,
			"filter_deny":  filterDeny,
		},
	}
	writeJSON(w, http.StatusOK, resp)
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
		if os.Getenv(f.EnvVar) != "" {
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
			normalized, err := store.NormalizeRouteFilters(filters)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid route filter: " + err.Error()})
				return
			}
			_ = s.store.SetGlobalRouteFilters(r.Context(), normalized) //nolint:errcheck // best-effort filter update in settings save
			if s.bgp != nil {
				if err := s.bgp.Reconcile(r.Context()); err != nil {
					logging.FromContext(r.Context()).Debug("bgp reconcile failed after global filter change", "error", err)
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
