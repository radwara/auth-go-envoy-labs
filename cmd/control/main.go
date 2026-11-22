package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"

	decision "authzlab/api/decision/v1"
	"authzlab/internal/policy"
	"authzlab/internal/transport"
	"github.com/jackc/pgx/v5/pgconn"
)

type api struct {
	store    policy.Store
	decision decision.DecisionServiceClient
}

func main() {
	ctx, cancel := transport.Context()
	defer cancel()
	db, err := policy.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	conn, err := transport.DecisionClient()
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	a := api{policy.Store{DB: db}, decision.NewDecisionServiceClient(conn)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			transport.Error(w, 503, "database unavailable")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /policies", a.list)
	mux.HandleFunc("POST /policies", a.create)
	mux.HandleFunc("GET /policies/{id}", a.byID)
	mux.HandleFunc("PUT /policies/{id}", a.byID)
	mux.HandleFunc("DELETE /policies/{id}", a.byID)
	mux.HandleFunc("POST /authorize", a.authorize)
	if err := transport.Serve(ctx, transport.Env("HTTP_ADDR", ":8081"), mux); err != nil {
		log.Fatal(err)
	}
}

func (a api) list(w http.ResponseWriter, r *http.Request) {
	var after int64
	if raw := r.URL.Query().Get("after"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 0 {
			transport.Error(w, 400, "invalid after cursor")
			return
		}
		after = id
	}
	rules, err := a.store.List(r.Context(), after)
	respond(w, 200, rules, err)
}

func (a api) create(w http.ResponseWriter, r *http.Request) {
	input, ok := readRule(w, r)
	if !ok {
		return
	}
	rule, err := a.store.Create(r.Context(), input)
	if err == nil {
		w.Header().Set("Location", "/policies/"+strconv.FormatInt(rule.ID, 10))
	}
	respond(w, 201, rule, err)
}

func (a api) byID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		transport.Error(w, 400, "invalid policy ID")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		rule, err := a.store.Get(r.Context(), id)
		respond(w, 200, rule, err)
	case http.MethodPut:
		input, ok := readRule(w, r)
		if !ok {
			return
		}
		rule, err := a.store.Update(r.Context(), id, input)
		respond(w, 200, rule, err)
	case http.MethodDelete:
		respond(w, 204, nil, a.store.Delete(r.Context(), id))
	}
}

func (a api) authorize(w http.ResponseWriter, r *http.Request) {
	var m policy.Match
	if !transport.Read(w, r, &m) {
		return
	}
	if !m.Valid() {
		transport.Error(w, 400, "valid subject, action and resource required")
		return
	}
	result, err := transport.Check(r.Context(), a.decision, m)
	if err != nil {
		log.Printf("decision check: %v", err)
		transport.Error(w, 503, "decision service unavailable")
		return
	}
	transport.Write(w, 200, map[string]any{"allowed": result.GetAllowed(), "reason": result.GetReason(), "replica": result.GetReplica()})
}

func readRule(w http.ResponseWriter, r *http.Request) (policy.Rule, bool) {
	var input struct {
		policy.Match
		Effect string `json:"effect"`
	}
	if !transport.Read(w, r, &input) {
		return policy.Rule{}, false
	}
	rule := policy.Rule{Match: input.Match, Effect: input.Effect}
	if !rule.Valid() {
		transport.Error(w, 400, "valid subject, action, resource and allow or deny effect required")
		return policy.Rule{}, false
	}
	return rule, true
}

func respond(w http.ResponseWriter, code int, data any, err error) {
	var pgError *pgconn.PgError
	switch {
	case err == nil && code == 204:
		w.WriteHeader(204)
	case err == nil:
		transport.Write(w, code, data)
	case errors.Is(err, sql.ErrNoRows):
		transport.Error(w, 404, "policy not found")
	case errors.As(err, &pgError) && pgError.Code == "23505":
		transport.Error(w, 409, "policy already exists for this subject, action and resource")
	default:
		log.Printf("policy operation: %v", err)
		transport.Error(w, 503, "policy store unavailable")
	}
}
