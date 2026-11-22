package main

import (
	"context"
	"log"
	"net/http"
	"strconv"

	decision "authzlab/api/decision/v1"
	"authzlab/internal/transport"
	"authzlab/internal/worker"
)

func main() {
	ctx, cancel := transport.Context()
	defer cancel()
	conn, err := transport.DecisionClient()
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	client := decision.NewDecisionServiceClient(conn)
	pool := worker.New(4, 16, 100, func(ctx context.Context, input worker.Input) (*decision.CheckResponse, error) {
		return transport.Check(ctx, client, input)
	})
	defer pool.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var input worker.Input
		if !transport.Read(w, r, &input) {
			return
		}
		if !input.Valid() {
			transport.Error(w, 400, "valid subject, action and resource required")
			return
		}
		job, err := pool.Submit(input)
		if err != nil {
			w.Header().Set("Retry-After", "1")
			transport.Error(w, 503, err.Error())
			return
		}
		w.Header().Set("Location", "/jobs/"+strconv.FormatUint(job.ID, 10))
		transport.Write(w, 202, job)
	})
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		if err != nil || id == 0 {
			transport.Error(w, 400, "invalid job ID")
			return
		}
		job, ok := pool.Get(id)
		if !ok {
			transport.Error(w, 404, "job not found")
			return
		}
		transport.Write(w, 200, job)
	})
	if err := transport.Serve(ctx, transport.Env("HTTP_ADDR", ":8082"), mux); err != nil {
		log.Print(err)
	}
}
