package web

import "net/http"

type bgpStatusJSON struct {
	Running        bool   `json:"running"`
	RestartPending bool   `json:"restart_pending"`
	LastError      string `json:"last_error,omitempty"`
}

func (s *Server) bgpStatusJSON() bgpStatusJSON {
	running, lastErr := s.bgp.Status()
	resp := bgpStatusJSON{
		Running:        running,
		RestartPending: s.restartPending.Load(),
	}
	if lastErr != nil {
		resp.LastError = lastErr.Error()
	}
	return resp
}

// apiBGPStatus handles GET /api/admin/bgp/status.
func (s *Server) apiBGPStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.bgpStatusJSON())
}

// apiBGPReload handles POST /api/admin/bgp/reload. Applies the
// restart-only BGP settings (LocalASN, RouterID, BGPPort,
// LocalAddressV4/V6, ActiveDial, DynamicPeerMD5Match/QueueNum) by tearing
// down and rebuilding the speaker — the same action whether the admin is
// applying a pending change or retrying after a failed start.
func (s *Server) apiBGPReload(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // full speaker rebuild + reconcile can exceed WriteTimeout
	err := s.bgp.ReloadPeers(r.Context())
	if err == nil {
		s.restartPending.Store(false)
	}
	status := s.bgpStatusJSON()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, status)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
