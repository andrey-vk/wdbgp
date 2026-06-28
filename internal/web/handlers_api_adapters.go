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

	"github.com/andrey-vk/wdbgp/internal/store"
)

type adapterJSON struct {
	ID             int64  `json:"id"`
	Key            string `json:"key"`
	Name           string `json:"name"`
	Language       string `json:"language"`
	APIVersion     int    `json:"api_version"`
	Source         string `json:"source"`
	Revision       int64  `json:"revision"`
	BuiltIn        bool   `json:"builtin"`
	ForkedFrom     string `json:"forked_from,omitempty"`
	ForkedVersion  int64  `json:"forked_version,omitempty"`
	RequiresReview bool   `json:"requires_review"`
}

func adapterToJSON(a store.FeedAdapter) adapterJSON {
	aj := adapterJSON{
		ID: a.ID, Key: a.Key, Name: a.Name,
		Language: a.Language, APIVersion: a.APIVersion,
		Source: a.Source,
		Revision: a.Revision, BuiltIn: a.BuiltIn,
		ForkedFrom: a.ForkedFrom, ForkedVersion: a.ForkedVersion,
	}
	if a.ForkedFrom != "" {
		if store.ForkedAdapterNeedsReview(a.ForkedFrom, a.ForkedVersion) {
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
		result[i] = adapterToJSON(a)
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
	writeJSON(w, http.StatusOK, adapterToJSON(adapter))
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
	created, err := s.store.AddFeedAdapter(r.Context(), adapter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, adapterToJSON(created))
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
	if old, err := s.store.FeedAdapter(r.Context(), id); err == nil && !old.BuiltIn {
		backupAdapterSource(old, s.cfg.AdapterBackupDir, s.cfg.AdapterBackupMax)
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
	writeJSON(w, http.StatusOK, adapterToJSON(updated))
}

func (s *Server) apiAdaptersDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid adapter ID"})
		return
	}
	adapter, err := s.store.FeedAdapter(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if adapter.BuiltIn {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Built-in adapters cannot be deleted."})
		return
	}
	backupAdapterSource(adapter, s.cfg.AdapterBackupDir, s.cfg.AdapterBackupMax)
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
	writeJSON(w, http.StatusCreated, adapterToJSON(forked))
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
	if adapter.ForkedFrom == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Not a forked adapter"})
		return
	}
	// Update forked_version to match current builtin version
	if v, ok := store.BuiltInAdapterVersion(adapter.ForkedFrom); ok {
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
	writeJSON(w, http.StatusOK, adapterToJSON(updated))
}

func backupAdapterSource(adapter store.FeedAdapter, backupDir string, maxCopies int) {
	if backupDir == "" || adapter.Source == "" {
		return
	}
	if err := os.MkdirAll(backupDir, 0755); err != nil { //nolint:gosec // container filesystem, single user
		log.Printf("WARNING: backup adapter mkdir: %v", err)
		return
	}
	name := fmt.Sprintf("%s_r%d_%s.js", adapter.Key, adapter.Revision, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte(adapter.Source), 0644); err != nil { //nolint:gosec // backup files on server, single user
		log.Printf("WARNING: backup adapter write: %v", err)
		return
	}
	pruneAdapterBackups(adapter.Key, backupDir, maxCopies)
}

func pruneAdapterBackups(key, dir string, max int) {
	if max <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no entries to prune
	}
	var files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), key+"_") {
			files = append(files, e.Name())
		}
	}
	if len(files) <= max {
		return
	}
	sort.Strings(files)
	for _, f := range files[:len(files)-max] {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			log.Printf("WARNING: remove backup: %v", err)
		}
	}
}
