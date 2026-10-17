//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"time"
)

const DefaultPipeName = "xdl.v1.sock"

type Handler func(ctx context.Context, req Request) Response

type Server struct {
	PipeName string
	Handler  Handler
}

func NewServer(handler Handler) *Server {
	return &Server{PipeName: DefaultPipeName, Handler: handler}
}

func (s *Server) Start(ctx context.Context) error {
	return fmt.Errorf("named pipe server not implemented on this OS yet")
}

func (s *Server) Broadcast(ev EventMessage) {}

func (s *Server) Close() error { return nil }

func Dial(timeout time.Duration) (net.Conn, error) {
	return nil, fmt.Errorf("not implemented")
}

func Call(timeout time.Duration, req Request) (Response, error) {
	return Response{}, fmt.Errorf("not implemented")
}

func Ping(timeout time.Duration) error {
	return fmt.Errorf("not implemented")
}
