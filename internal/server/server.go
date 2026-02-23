package server

import (
	"net/http"
	"time"
)

func New(addr string, healthHandler http.HandlerFunc, ingestHandlers *IngestHandlers) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)
	if ingestHandlers != nil {
		mux.HandleFunc("POST /v1/traces", ingestHandlers.PostTrace)
		mux.HandleFunc("POST /v1/errors", ingestHandlers.PostError)
	}

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
