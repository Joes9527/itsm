package workerhealth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server exposes only internal worker health and metrics endpoints. It never
// imports application routing or business HTTP handlers.
type Server struct {
	address   string
	readiness func(context.Context) error
	metrics   http.Handler
	handler   http.Handler
}

func New(address string, readiness func(context.Context) error, metrics http.Handler) *Server {
	server := &Server{address: address, readiness: readiness, metrics: metrics}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealth)
	mux.HandleFunc("/readyz", server.handleReady)
	if metrics == nil {
		metrics = promhttp.Handler()
	}
	mux.Handle("/metrics", metrics)
	server.handler = mux
	return server
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: s.handler}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		serveErr := <-serveResult
		if shutdownErr != nil {
			return shutdownErr
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("ok\n"))
}

func (s *Server) handleReady(writer http.ResponseWriter, request *http.Request) {
	if s.readiness != nil && s.readiness(request.Context()) != nil {
		http.Error(writer, "not_ready", http.StatusServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("ready\n"))
}
