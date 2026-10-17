//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func TestSubscriptionSurvivesIdleTimeout(t *testing.T) {
	s, cancel := startTestServer(t)
	defer cancel()
	s.RequestTimeout = 300 * time.Millisecond

	conn := dialPipe(t, s.PipeName)
	defer conn.Close()

	if err := WriteJSON(conn, Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodSubscribe}); err != nil {
		t.Fatal(err)
	}
	var ack Response
	if err := ReadJSON(conn, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Error != nil {
		t.Fatalf("subscribe error: %+v", ack.Error)
	}

	time.Sleep(600 * time.Millisecond)

	s.Broadcast(EventMessage{Method: "event.task.progress", Params: map[string]int{"n": 1}})

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ev EventMessage
	if err := ReadJSON(conn, &ev); err != nil {
		t.Fatalf("expected event after idle: %v", err)
	}
	if ev.Method != "event.task.progress" {
		t.Fatalf("method %s", ev.Method)
	}
}

func TestSubscriberCleanup(t *testing.T) {
	s, cancel := startTestServer(t)
	defer cancel()

	conn := dialPipe(t, s.PipeName)
	if err := WriteJSON(conn, Request{JSONRPC: JSONRPCVersion, ID: 1, Method: MethodSubscribe}); err != nil {
		t.Fatal(err)
	}
	var ack Response
	if err := ReadJSON(conn, &ack); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for s.SubscriberCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.SubscriberCount() != 1 {
		t.Fatalf("subs %d", s.SubscriberCount())
	}

	_ = conn.Close()

	deadline = time.Now().Add(2 * time.Second)
	for s.SubscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.SubscriberCount() != 0 {
		t.Fatalf("subscriber not removed, count=%d", s.SubscriberCount())
	}
}

func startTestServer(t *testing.T) (*Server, context.CancelFunc) {
	t.Helper()
	s := NewServer(func(ctx context.Context, req Request) Response {
		return OK(req.ID, map[string]string{"status": "ok"})
	})
	s.PipeName = fmt.Sprintf(`\\.\pipe\xdl-test-%d`, time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		_ = s.Close()
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := dialRaw(s.PipeName, 150*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return s, cancel
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err)
			}
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return nil, cancel
}

func dialPipe(t *testing.T, name string) net.Conn {
	t.Helper()
	c, err := dialRaw(name, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func dialRaw(name string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return winio.DialPipeContext(ctx, name)
}
