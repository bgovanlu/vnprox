// SPDX-License-Identifier: Apache-2.0

package pvemock

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type taskStatusResponse struct {
	UPID       string `json:"upid"`
	Node       string `json:"node"`
	Type       string `json:"type"`
	User       string `json:"user"`
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus,omitempty"`
	StartTime  int64  `json:"starttime"`
	EndTime    int64  `json:"endtime,omitempty"`
}

func (srv *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	upid := taskUPIDParam(r)
	t, ok := srv.state.tasks.Get(upid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("task %q not found", upid))
		return
	}
	resp := taskStatusResponse{
		UPID:       t.UPID,
		Node:       t.Node,
		Type:       t.Type,
		User:       t.User,
		Status:     t.Status,
		ExitStatus: t.ExitStatus,
		StartTime:  t.StartTime.Unix(),
	}
	if !t.EndTime.IsZero() {
		resp.EndTime = t.EndTime.Unix()
	}
	writeData(w, http.StatusOK, resp)
}

func (srv *Server) handleTaskLog(w http.ResponseWriter, r *http.Request) {
	upid := taskUPIDParam(r)
	t, ok := srv.state.tasks.Get(upid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("task %q not found", upid))
		return
	}
	type logLine struct {
		T string `json:"t"`
		N int    `json:"n"`
	}
	lines := t.logLines()
	out := make([]logLine, len(lines))
	for i, l := range lines {
		out[i] = logLine{N: i + 1, T: l}
	}
	writeData(w, http.StatusOK, out)
}

// taskUPIDParam recovers the full UPID from the chi route param. UPIDs
// contain colons, which chi's {upid} wildcard captures fine as long as the
// route itself doesn't also split on colons, but we defensively rejoin in
// case a client URL-encoded segments individually.
func taskUPIDParam(r *http.Request) string {
	upid := chi.URLParam(r, "upid")
	return strings.TrimSpace(upid)
}
