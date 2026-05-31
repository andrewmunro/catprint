// Web UI + JSON API for the thermal printer. Mobile-first single page plus
// a Web Share Target so Android can share text straight to the printer.
//
// Routes:
//
//	GET  /                     single-page UI
//	GET  /manifest.json,/sw.js PWA assets
//	POST /print/text           {content, title?} -> enqueue markdown
//	POST /print/share          share-target form (text/title/url) -> enqueue
//	GET  /status               queue + reachability snapshot
//	GET  /jobs                 recent job log
//	POST /jobs/{id}/reprint    requeue an existing job's content
package web

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/synestry/catprint/jobs"
	"github.com/synestry/catprint/printer"
	"github.com/synestry/catprint/validate"
)

//go:embed static/*
var staticFS embed.FS

// Deps is what the handlers need from the rest of the app.
type Deps struct {
	Queue *printer.Queue
	Store *jobs.Store
}

// Handler returns an http.Handler serving the UI and API.
func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()

	sub, _ := fs.Sub(staticFS, "static")
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Serve index for root; delegate other GETs to the embedded file server.
		if r.URL.Path == "/" {
			http.ServeFileFS(w, r, sub, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("POST /print/text", d.handlePrintText)
	mux.HandleFunc("POST /print/share", d.handlePrintShare)
	mux.HandleFunc("GET /status", d.handleStatus)
	mux.HandleFunc("GET /jobs", d.handleJobs)
	mux.HandleFunc("POST /jobs/{id}/reprint", d.handleReprint)

	return mux
}

type printTextReq struct {
	Content string `json:"content"`
	Title   string `json:"title"`
}

type printResp struct {
	JobID      string               `json:"job_id"`
	Status     string               `json:"status"`
	Error      string               `json:"error,omitempty"`
	Violations []validate.Violation `json:"violations,omitempty"`
}

// composeMarkdown prepends an optional title as an H1.
func composeMarkdown(title, content string) string {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" {
		return content
	}
	return "# " + title + "\n\n" + content
}

func (d Deps) handlePrintText(w http.ResponseWriter, r *http.Request) {
	var req printTextReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	md := composeMarkdown(req.Title, req.Content)
	if strings.TrimSpace(md) == "" {
		writeErr(w, http.StatusBadRequest, "empty content")
		return
	}
	if res := validate.Validate(md); !res.OK() {
		writeJSON(w, http.StatusUnprocessableEntity, printResp{Error: res.Error, Violations: res.Violations})
		return
	}
	d.enqueueAndRespond(w, "web", md)
}

// handlePrintShare accepts the Android Web Share Target POST (form-encoded).
// Fields: title, text, url — we combine text + url into the body.
func (d Deps) handlePrintShare(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "bad form")
		return
	}
	title := r.FormValue("title")
	text := r.FormValue("text")
	url := r.FormValue("url")

	body := strings.TrimSpace(text)
	if url != "" {
		if body != "" {
			body += "\n\n"
		}
		body += url
	}
	md := composeMarkdown(title, body)
	if strings.TrimSpace(md) == "" {
		writeErr(w, http.StatusBadRequest, "nothing shared")
		return
	}
	// Share target is a navigation; validate then enqueue, redirect back to
	// the UI with a flag so the page can surface the outcome.
	if res := validate.Validate(md); !res.OK() {
		http.Redirect(w, r, "/?shared=invalid", http.StatusSeeOther)
		return
	}
	if _, err := d.Queue.Submit("web", md); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/?shared=1", http.StatusSeeOther)
}

// enqueueAndRespond submits and waits briefly for a terminal status.
func (d Deps) enqueueAndRespond(w http.ResponseWriter, source, md string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	j, err := d.Queue.SubmitAndWait(ctx, source, md)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, printResp{JobID: j.ID, Status: string(j.Status)})
}

type statusResp struct {
	Reachable        bool   `json:"reachable"`
	PrinterAddr      string `json:"printer_addr"`
	QueueDepth       int    `json:"queue_depth"`
	JobsPendingRetry int    `json:"jobs_pending_retry"`
	Note             string `json:"note"`
}

func (d Deps) handleStatus(w http.ResponseWriter, _ *http.Request) {
	queued, _ := d.Store.CountByStatus(jobs.StatusQueued)
	failed, _ := d.Store.CountByStatus(jobs.StatusFailed)
	writeJSON(w, http.StatusOK, statusResp{
		Reachable:        d.Queue.Addr() != "",
		PrinterAddr:      d.Queue.Addr(),
		QueueDepth:       queued,
		JobsPendingRetry: failed,
		Note:             "sent = BLE transmitted, not paper confirmed",
	})
}

type jobView struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Source    string `json:"source"`
	Status    string `json:"status"`
	FirstLine string `json:"first_line"`
	Error     string `json:"error,omitempty"`
}

func (d Deps) handleJobs(w http.ResponseWriter, _ *http.Request) {
	list, err := d.Store.List(50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]jobView, 0, len(list))
	for _, j := range list {
		views = append(views, jobView{
			ID:        j.ID,
			CreatedAt: j.CreatedAt.Format(time.RFC3339),
			Source:    j.Source,
			Status:    string(j.Status),
			FirstLine: firstLine(j.Content),
			Error:     j.Error,
		})
	}
	writeJSON(w, http.StatusOK, views)
}

func (d Deps) handleReprint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := d.Store.Requeue(id, "web")
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	d.Queue.Notify()
	writeJSON(w, http.StatusOK, printResp{JobID: j.ID, Status: string(j.Status)})
}

func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(ln, "#-*[ ]x"))
		if ln != "" {
			if len(ln) > 48 {
				return ln[:48] + "…"
			}
			return ln
		}
	}
	return "(empty)"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, printResp{Error: msg})
}
