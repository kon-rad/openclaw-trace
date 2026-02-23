package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/ingest"
)

type IngestEnqueuer interface {
	Enqueue(event ingest.Event) bool
}

type IngestHandlers struct {
	enqueuer IngestEnqueuer
}

type traceRequest struct {
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	InputText        string  `json:"input_text"`
	OutputText       string  `json:"output_text"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	LatencyMS        int     `json:"latency_ms"`
	Status           string  `json:"status"`
	ErrorType        string  `json:"error_type"`
	Metadata         string  `json:"metadata"`
}

type errorRequest struct {
	ErrorType  string `json:"error_type"`
	Message    string `json:"message"`
	StackTrace string `json:"stack_trace"`
	Severity   string `json:"severity"`
	Metadata   string `json:"metadata"`
}

func NewIngestHandlers(enqueuer IngestEnqueuer) *IngestHandlers {
	return &IngestHandlers{enqueuer: enqueuer}
}

func (h *IngestHandlers) PostTrace(w http.ResponseWriter, r *http.Request) {
	var req traceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Provider == "" || req.Model == "" {
		http.Error(w, "provider and model are required", http.StatusBadRequest)
		return
	}
	if req.Status == "" {
		req.Status = "ok"
	}

	h.enqueuer.Enqueue(ingest.Event{
		Kind:      ingest.EventKindTrace,
		CreatedAt: time.Now().UnixMilli(),
		Trace: &ingest.TracePayload{
			Provider:         req.Provider,
			Model:            req.Model,
			InputText:        req.InputText,
			OutputText:       req.OutputText,
			PromptTokens:     req.PromptTokens,
			CompletionTokens: req.CompletionTokens,
			TotalTokens:      req.TotalTokens,
			CostUSD:          req.CostUSD,
			LatencyMS:        req.LatencyMS,
			Status:           req.Status,
			ErrorType:        req.ErrorType,
			Metadata:         req.Metadata,
		},
	})

	w.WriteHeader(http.StatusAccepted)
}

func (h *IngestHandlers) PostError(w http.ResponseWriter, r *http.Request) {
	var req errorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.ErrorType == "" || req.Message == "" {
		http.Error(w, "error_type and message are required", http.StatusBadRequest)
		return
	}
	if req.Severity == "" {
		req.Severity = "error"
	}

	h.enqueuer.Enqueue(ingest.Event{
		Kind:      ingest.EventKindError,
		CreatedAt: time.Now().UnixMilli(),
		Error: &ingest.ErrorPayload{
			ErrorType:  req.ErrorType,
			Message:    req.Message,
			StackTrace: req.StackTrace,
			Severity:   req.Severity,
			Metadata:   req.Metadata,
		},
	})

	w.WriteHeader(http.StatusAccepted)
}
