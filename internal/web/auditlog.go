package web

import (
	"net/http"
	"strconv"
)

// adminActor builds the "admin:<ip>" actor string for an admin-triggered
// mutation. There is no per-admin identity in this codebase today (the
// admin session is a single shared password/token, see session.go) — IP
// is the only "who" signal available.
func (s *Server) adminActor(r *http.Request) string {
	return "admin:" + s.clientIP(r)
}

// userActor builds the "user:<id>" actor string for a self-service
// mutation made by an authenticated end user.
func userActor(userID int64) string {
	return "user:" + strconv.FormatInt(userID, 10)
}
