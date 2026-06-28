package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/store"
)

// apiDebugCIDR handles GET /api/admin/debug?cidr=...&mode=...
func (s *Server) apiDebugCIDR(w http.ResponseWriter, r *http.Request) {
	modeID := store.DefaultCatalogModeID
	if rawMode := strings.TrimSpace(r.URL.Query().Get("mode")); rawMode != "" {
		var err error
		modeID, err = strconv.ParseInt(rawMode, 10, 64)
		if err != nil || modeID <= 0 {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "invalid catalog mode"})
			return
		}
	}
	cidr := strings.TrimSpace(r.URL.Query().Get("cidr"))
	if cidr == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "cidr query parameter is required"})
		return
	}
	result, err := s.debugCIDR(r.Context(), cidr, modeID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
