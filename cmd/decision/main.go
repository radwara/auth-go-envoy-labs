package main

import (
	"context"
	"log"
	"net"
	"time"

	decision "authzlab/api/decision/v1"
	"authzlab/internal/policy"
	"authzlab/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type server struct {
	decision.UnimplementedDecisionServiceServer
	lookup  func(context.Context, policy.Match) (string, error)
	replica string
}

func (s server) Check(ctx context.Context, r *decision.CheckRequest) (*decision.CheckResponse, error) {
	match := policy.Match{Subject: r.GetSubject(), Action: r.GetAction(), Resource: r.GetResource()}
	if !match.Valid() {
		return nil, status.Error(codes.InvalidArgument, "valid subject, action and resource required")
	}
	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	effect, err := s.lookup(ctx, match)
	if err != nil {
		log.Printf("policy lookup: %v", err)
		return nil, status.Error(codes.Unavailable, "policy store unavailable")
	}
	reason := "no_matching_policy"
	if effect != "" {
		reason = "explicit_" + effect
	}
	return &decision.CheckResponse{Allowed: effect == "allow", Reason: reason, Replica: s.replica}, nil
}

func main() {
	ctx, cancel := transport.Context()
	defer cancel()
	db, err := policy.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	listener, err := net.Listen("tcp", transport.Env("GRPC_ADDR", ":9090"))
	if err != nil {
		log.Fatal(err)
	}

	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(8192))
	decision.RegisterDecisionServiceServer(grpcServer, server{lookup: policy.Store{DB: db}.Effect, replica: transport.Env("REPLICA", "decision-local")})
	probe := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, probe)
	go func() {
		<-ctx.Done()
		probe.Shutdown()
		timer := time.AfterFunc(3*time.Second, grpcServer.Stop)
		defer timer.Stop()
		grpcServer.GracefulStop()
	}()
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatal(err)
	}
}
