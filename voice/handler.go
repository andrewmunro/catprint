// Voice webhook: turns a spoken query into printer-ready markdown via Gemini,
// validates it, and enqueues it. Phase 5 (Go path only — no Workspace sidecar).
//
// A Google Home Action (or any caller) POSTs {"query": "..."} to /voice. We
// ask Gemini — with the GoogleSearch tool for fresh facts — to compose markdown
// conforming to the printer's constraints, then validate and print it.
package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/synestry/catprint/printer"
	"github.com/synestry/catprint/validate"
)

// defaultModel is used when the GEMINI_MODEL env var is unset.
const defaultModel = "gemini-2.0-flash"

// modelName returns the Gemini model, overridable via GEMINI_MODEL so you can
// switch (e.g. gemini-2.0-flash-lite, gemini-1.5-flash) without a rebuild.
func modelName() string {
	if m := os.Getenv("GEMINI_MODEL"); m != "" {
		return m
	}
	return defaultModel
}

// Deps is what the handler needs from the rest of the app.
type Deps struct {
	Queue  *printer.Queue
	APIKey string // GOOGLE_API_KEY; if empty, /voice returns 503
}

// Handler returns the /voice HTTP handler.
func Handler(d Deps) http.Handler {
	return http.HandlerFunc(d.serve)
}

type voiceReq struct {
	Query string `json:"query"`
}

type voiceResp struct {
	JobID    string `json:"job_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Error    string `json:"error,omitempty"`
	Spoken   string `json:"spoken,omitempty"` // short reply for the voice assistant
}

func (d Deps) serve(w http.ResponseWriter, r *http.Request) {
	if d.APIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, voiceResp{
			Error: "voice disabled: GOOGLE_API_KEY not set", Spoken: "Voice printing isn't configured yet.",
		})
		return
	}

	query := extractQuery(r)
	if strings.TrimSpace(query) == "" {
		writeJSON(w, http.StatusBadRequest, voiceResp{Error: "empty query", Spoken: "I didn't catch what to print."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	md, err := d.compose(ctx, query)
	if err != nil {
		log.Printf("voice: compose: %v", err)
		writeJSON(w, http.StatusBadGateway, voiceResp{Error: err.Error(), Spoken: "Sorry, I couldn't compose that."})
		return
	}

	j, err := d.Queue.SubmitAndWait(ctx, "voice", md)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, voiceResp{Error: err.Error(), Spoken: "I made it but couldn't reach the printer."})
		return
	}
	writeJSON(w, http.StatusOK, voiceResp{
		JobID: j.ID, Status: string(j.Status), Markdown: md,
		Spoken: spokenFor(string(j.Status)),
	})
}

// extractQuery pulls the spoken text from JSON {query} or form/query param.
func extractQuery(r *http.Request) string {
	if r.Header.Get("Content-Type") == "application/json" || strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var req voiceReq
		if json.NewDecoder(r.Body).Decode(&req) == nil {
			return req.Query
		}
		return ""
	}
	if v := r.FormValue("query"); v != "" {
		return v
	}
	return r.URL.Query().Get("query")
}

// compose asks Gemini for printer markdown, validating and retrying once with
// the violations fed back so the model can self-correct.
func (d Deps) compose(ctx context.Context, query string) (string, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  d.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return "", fmt.Errorf("genai client: %w", err)
	}

	caps, _ := json.Marshal(printer.CurrentCapabilities())
	system := "You turn a spoken request into a short document to print on a 58mm thermal receipt printer. " +
		"Respond with ONLY the markdown to print — no code fences around the whole thing, no commentary, no explanations. " +
		"Obey these printer capabilities exactly:\n" + string(caps) + "\n" +
		"Keep it concise and useful. Use a short '# Title', bullets, and checkboxes where natural. " +
		"Emoji are welcome. Never exceed the line or line-count limits."

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(system, genai.RoleUser),
		Tools:             []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
		Temperature:       genai.Ptr[float32](0.4),
	}

	prompt := query
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := client.Models.GenerateContent(ctx, modelName(), genai.Text(prompt), cfg)
		if err != nil {
			return "", fmt.Errorf("generate: %w", err)
		}
		md := cleanMarkdown(resp.Text())
		if md == "" {
			return "", fmt.Errorf("empty response from model")
		}
		res := validate.Validate(md)
		if res.OK() {
			return md, nil
		}
		// Feed violations back for one corrective retry.
		vb, _ := json.Marshal(res.Violations)
		prompt = query + "\n\nYour previous attempt was rejected by the printer validator:\n" +
			string(vb) + "\nFix every issue and return only the corrected markdown."
	}
	return "", fmt.Errorf("could not produce valid markdown after retry")
}

// cleanMarkdown strips an outer ```...``` fence if the model wrapped the whole
// reply in one (we asked it not to, but models often do anyway).
func cleanMarkdown(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func spokenFor(status string) string {
	switch status {
	case "sent":
		return "Printed."
	case "queued":
		return "Queued — it'll print when the printer's reachable."
	case "failed":
		return "The printer didn't respond."
	default:
		return "Done."
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
