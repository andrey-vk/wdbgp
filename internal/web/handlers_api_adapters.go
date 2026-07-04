package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type adapterJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Language       string `json:"language"`
	APIVersion     int    `json:"api_version"`
	Source         string `json:"source"`
	Revision       int64  `json:"revision"`
	BuiltIn        bool   `json:"builtin"`
	ForkedFrom     int64  `json:"forked_from,omitempty"`
	ForkedVersion  int64  `json:"forked_version,omitempty"`
	RequiresReview bool   `json:"requires_review"`
}

func (s *Server) adapterToJSON(a store.FeedAdapter) adapterJSON {
	aj := adapterJSON{
		ID: a.ID, Name: a.Name,
		Language: a.Language, APIVersion: a.APIVersion,
		Source:   a.Source,
		Revision: a.Revision, BuiltIn: a.BuiltIn,
		ForkedFrom: a.ForkedFrom, ForkedVersion: a.ForkedVersion,
	}
	if a.ForkedFrom != 0 {
		if s.store.ForkedAdapterNeedsReview(a.ForkedFrom, a.ForkedVersion) {
			aj.RequiresReview = true
		}
	}
	return aj
}

func (s *Server) apiAdaptersList(w http.ResponseWriter, r *http.Request) {
	adapters, err := s.store.FeedAdapters(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load adapters"})
		return
	}
	result := make([]adapterJSON, len(adapters))
	for i, a := range adapters {
		result[i] = s.adapterToJSON(a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"adapters": result})
}

func (s *Server) apiAdaptersGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter ID"})
		return
	}
	adapter, err := s.store.FeedAdapter(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Adapter not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load adapter"})
		return
	}
	writeJSON(w, http.StatusOK, s.adapterToJSON(adapter))
}

func (s *Server) apiAdaptersCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	adapter := store.FeedAdapter{
		Name: body.Name, Source: body.Source,
		Language: "javascript", APIVersion: 1,
	}
	if err := store.ValidateFeedAdapter(adapter); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if err := feeds.ValidateAdapterSource(adapter.Source, s.settings.JSMaxSourceBytes.Get()); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	created, err := s.store.AddFeedAdapter(r.Context(), adapter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, s.adapterToJSON(created))
}

func (s *Server) apiAdaptersUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter ID"})
		return
	}
	// Reject built-in adapters
	adapter, err := s.store.FeedAdapter(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Adapter not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load adapter"})
		return
	}
	if adapter.BuiltIn {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Built-in adapters cannot be edited. Use fork to create an editable copy."})
		return
	}

	var body struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	update := store.FeedAdapter{
		ID: id, Name: body.Name, Source: body.Source,
		ForkedVersion: adapter.ForkedVersion, // preserve fork point
	}
	if err := store.ValidateFeedAdapter(update); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if err := feeds.ValidateAdapterSource(update.Source, s.settings.JSMaxSourceBytes.Get()); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if old, err := s.store.FeedAdapter(r.Context(), id); err == nil && !old.BuiltIn {
		backupAdapterSource(old, s.settings.AdapterBackupDir.Get(), s.settings.AdapterBackupMax.Get())
	}
	if err := s.store.UpdateFeedAdapter(r.Context(), update); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	updated, err := s.store.FeedAdapter(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load updated adapter"})
		return
	}
	writeJSON(w, http.StatusOK, s.adapterToJSON(updated))
}

func (s *Server) apiAdaptersDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter ID"})
		return
	}
	adapter, err := s.store.FeedAdapter(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Adapter not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if adapter.BuiltIn {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Built-in adapters cannot be deleted."})
		return
	}
	backupAdapterSource(adapter, s.settings.AdapterBackupDir.Get(), s.settings.AdapterBackupMax.Get())
	if err := s.store.DeleteFeedAdapter(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// apiAdaptersFork handles POST /api/admin/adapters/{id}/fork
func (s *Server) apiAdaptersFork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter ID"})
		return
	}
	// Load source to check it exists and is forkable
	if _, err := s.store.FeedAdapter(r.Context(), id); err != nil {
		if store.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Adapter not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load adapter"})
		}
		return
	}
	forked, err := s.store.ForkAdapter(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, s.adapterToJSON(forked))
}

// apiAdaptersAcknowledge handles POST /api/admin/adapters/{id}/acknowledge
func (s *Server) apiAdaptersAcknowledge(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter ID"})
		return
	}
	adapter, err := s.store.FeedAdapter(r.Context(), id)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "Adapter not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load adapter"})
		return
	}
	if adapter.ForkedFrom == 0 {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Not a forked adapter"})
		return
	}
	// Update forked_version to match current builtin version
	if v, ok := s.store.BuiltInAdapterVersion(adapter.ForkedFrom); ok {
		adapter.ForkedVersion = v
	}
	if err := s.store.UpdateFeedAdapter(r.Context(), adapter); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	updated, err := s.store.FeedAdapter(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to load adapter"})
		return
	}
	writeJSON(w, http.StatusOK, s.adapterToJSON(updated))
}

func backupAdapterSource(adapter store.FeedAdapter, backupDir string, maxCopies int) {
	if backupDir == "" || adapter.Source == "" {
		return
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil { //nolint:gosec // container filesystem, single user
		log.Printf("WARNING: backup adapter mkdir: %v", err)
		return
	}
	name := fmt.Sprintf("%d_r%d_%s.js", adapter.ID, adapter.Revision, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte(adapter.Source), 0644); err != nil { //nolint:gosec // backup files on server, single user
		log.Printf("WARNING: backup adapter write: %v", err)
		return
	}
	pruneAdapterBackups(adapter, backupDir, maxCopies)
}

// legacyAdapterKey reconstructs the key that feed_adapters.key used to be
// auto-assigned before that column was dropped, so backup files written
// under the old {key}_r{rev}_{ts}.js naming (before backups switched to
// {id}_r{rev}_{ts}.js) still get swept into rotation instead of silently
// escaping AdapterBackupMax forever. Built-in adapters can never have
// backups (editing them is blocked in apiAdaptersUpdate), and every
// non-built-in adapter always got its key auto-generated from name this
// same way, so this reconstruction is exact, not a guess.
func legacyAdapterKey(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, name)
}

// backupTimestamp extracts the sortable timestamp suffix from a backup
// filename ({prefix}_r{rev}_{timestamp}.js), so files can be ordered
// chronologically even when old (key-prefixed) and new (id-prefixed)
// backups for the same adapter are mixed together.
func backupTimestamp(filename string) string {
	parts := strings.Split(strings.TrimSuffix(filename, ".js"), "_")
	return parts[len(parts)-1]
}

func pruneAdapterBackups(adapter store.FeedAdapter, dir string, max int) {
	if max <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no entries to prune
	}
	idPrefix := strconv.FormatInt(adapter.ID, 10) + "_"
	legacyPrefix := legacyAdapterKey(adapter.Name) + "_"
	var files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), idPrefix) || strings.HasPrefix(e.Name(), legacyPrefix) {
			files = append(files, e.Name())
		}
	}
	if len(files) <= max {
		return
	}
	sort.Slice(files, func(i, j int) bool {
		return backupTimestamp(files[i]) < backupTimestamp(files[j])
	})
	for _, f := range files[:len(files)-max] {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			log.Printf("WARNING: remove backup: %v", err)
		}
	}
}
