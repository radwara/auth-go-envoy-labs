package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	decision "authzlab/api/decision/v1"
	"authzlab/internal/policy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestCheckOverGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	decision.RegisterDecisionServiceServer(grpcServer, server{
		replica: "test-replica",
		lookup: func(_ context.Context, m policy.Match) (string, error) {
			switch m.Subject {
			case "alice":
				return "allow", nil
			case "bob":
				return "deny", nil
			case "db-error":
				return "", errors.New("connection reset")
			default:
				return "", nil
			}
		},
	})
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := decision.NewDecisionServiceClient(conn)

	for _, tc := range []struct {
		name, subject, reason string
		allowed               bool
		code                  codes.Code
	}{
		{"allow", "alice", "explicit_allow", true, codes.OK},
		{"deny", "bob", "explicit_deny", false, codes.OK},
		{"no match", "charlie", "no_matching_policy", false, codes.OK},
		{"invalid", "", "", false, codes.InvalidArgument},
		{"database unavailable", "db-error", "", false, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			response, err := client.Check(ctx, &decision.CheckRequest{Subject: tc.subject, Action: "read", Resource: "document:7"})
			if status.Code(err) != tc.code {
				t.Fatalf("Check error = %v, want %s", err, tc.code)
			}
			if tc.code == codes.OK && (response.GetAllowed() != tc.allowed || response.GetReason() != tc.reason || response.GetReplica() != "test-replica") {
				t.Fatalf("Check response = %v", response)
			}
		})
	}
}
