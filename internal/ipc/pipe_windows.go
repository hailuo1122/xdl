//go:build windows

package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const DefaultPipeName = `\\.\pipe\xdl.v1`

const (
	defaultRequestTimeout  = 5 * time.Minute
	subscriberWriteTimeout = 2 * time.Second
	subscriberQueueSize    = 32
)

// pipeSecurityDescriptor allows only the interactive user (and SYSTEM) to
// connect, and labels the pipe at Medium integrity so a non-elevated browser
// native-messaging host can reach an elevated XDL instance. Without the ML
// ACE, High-IL servers are unreachable from Chrome/Edge and the extension
// reports APP_STARTING / "启动超时".
func pipeSecurityDescriptor() string {
	sid := currentUserSIDString()
	if sid == "" {
		return "D:P(A;;GA;;;OW)(A;;GA;;;SY)S:(ML;;NW;;;ME)"
	}
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)S:(ML;;NW;;;ME)", sid)
}

func currentUserSIDString() string {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return ""
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return ""
	}
	return user.User.Sid.String()
}

type Handler func(ctx context.Context, req Request) Response

type Server struct {
	PipeName       string
	Handler        Handler
	OnEvent        func(EventMessage) // optional fan-out hook
	RequestTimeout time.Duration      // read deadline for non-subscribe requests; 0 = 5 minutes

	mu      sync.Mutex
	subs    map[net.Conn]*subConn
	ln      net.Listener
	running bool
}

type subConn struct {
	conn    net.Conn
	ch      chan EventMessage
	done    chan struct{}
	once    sync.Once
	writeMu sync.Mutex
}

func NewServer(handler Handler) *Server {
	return &Server{
		PipeName: DefaultPipeName,
		Handler:  handler,
		subs:     make(map[net.Conn]*subConn),
	}
}

func (s *Server) requestTimeout() time.Duration {
	if s.RequestTimeout > 0 {
		return s.RequestTimeout
	}
	return defaultRequestTimeout
}

// SubscriberCount reports live event subscriptions. Used by tests.
func (s *Server) SubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (s *Server) Start(ctx context.Context) error {
	cfg := &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor(),
		MessageMode:        false,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	}
	ln, err := winio.ListenPipe(s.PipeName, cfg)
	if err != nil {
		return fmt.Errorf("listen pipe: %w", err)
	}
	s.mu.Lock()
	s.ln = ln
	s.running = true
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	subscribed := false
	defer func() {
		s.removeSub(conn)
		_ = conn.Close()
	}()
	for {
		if !subscribed {
			_ = conn.SetReadDeadline(time.Now().Add(s.requestTimeout()))
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		var req Request
		if err := ReadJSON(conn, &req); err != nil {
			return
		}
		if req.JSONRPC == "" {
			req.JSONRPC = JSONRPCVersion
		}
		if req.Method == MethodSubscribe {
			sc := s.addSub(conn)
			subscribed = true
			if err := sc.writeJSON(OK(req.ID, map[string]string{"status": "subscribed"})); err != nil {
				return
			}
			continue
		}
		resp := s.Handler(ctx, req)
		if subscribed {
			if sc := s.lookupSub(conn); sc != nil {
				if err := sc.writeJSON(resp); err != nil {
					return
				}
				continue
			}
		}
		if err := WriteJSON(conn, resp); err != nil {
			return
		}
	}
}

func (s *Server) addSub(conn net.Conn) *subConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc, ok := s.subs[conn]; ok {
		return sc
	}
	sc := &subConn{
		conn: conn,
		ch:   make(chan EventMessage, subscriberQueueSize),
		done: make(chan struct{}),
	}
	s.subs[conn] = sc
	go s.writeLoop(sc)
	return sc
}

func (s *Server) lookupSub(conn net.Conn) *subConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subs[conn]
}

func (s *Server) removeSub(conn net.Conn) {
	s.mu.Lock()
	sc, ok := s.subs[conn]
	if ok {
		delete(s.subs, conn)
	}
	s.mu.Unlock()
	if ok {
		sc.once.Do(func() { close(sc.done) })
	}
}

func (s *Server) writeLoop(sc *subConn) {
	for {
		select {
		case <-sc.done:
			return
		case ev, ok := <-sc.ch:
			if !ok {
				return
			}
			if err := sc.writeJSON(ev); err != nil {
				s.removeSub(sc.conn)
				_ = sc.conn.Close()
				return
			}
		}
	}
}

func (sc *subConn) writeJSON(v interface{}) error {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	_ = sc.conn.SetWriteDeadline(time.Now().Add(subscriberWriteTimeout))
	return WriteJSON(sc.conn, v)
}

func (s *Server) Broadcast(ev EventMessage) {
	if ev.JSONRPC == "" {
		ev.JSONRPC = JSONRPCVersion
	}
	s.mu.Lock()
	snap := make([]*subConn, 0, len(s.subs))
	for _, sc := range s.subs {
		snap = append(snap, sc)
	}
	s.mu.Unlock()

	var slow []net.Conn
	for _, sc := range snap {
		select {
		case <-sc.done:
			continue
		case sc.ch <- ev:
		default:
			slow = append(slow, sc.conn)
		}
	}
	for _, c := range slow {
		s.removeSub(c)
		_ = c.Close()
	}
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

// Dial connects to the app pipe with timeout.
func Dial(timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return winio.DialPipeContext(ctx, DefaultPipeName)
}

// Call sends one request and waits for one response (short connection).
func Call(timeout time.Duration, req Request) (Response, error) {
	if req.JSONRPC == "" {
		req.JSONRPC = JSONRPCVersion
	}
	conn, err := Dial(timeout)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := WriteJSON(conn, req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := ReadJSON(conn, &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

// Ping checks if the main app is listening.
func Ping(timeout time.Duration) error {
	resp, err := Call(timeout, Request{Method: MethodPing, ID: 1})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc error: %s", resp.Error.Message)
	}
	return nil
}

func EncodeRequest(method string, id interface{}, params interface{}) (Request, error) {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return Request{}, err
		}
		raw = b
	}
	return Request{JSONRPC: JSONRPCVersion, ID: id, Method: method, Params: raw}, nil
}
