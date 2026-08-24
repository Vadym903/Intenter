package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Handler answers one request. Returning a *Error produces a protocol error
// response; any other error becomes INTERNAL.
type Handler func(ctx context.Context, req *Request) (any, error)

// Server accepts connections and dispatches one request per connection
// (§9.3 step 5, §10.2).
type Server struct {
	listener Listener
	logger   *slog.Logger
	timeout  time.Duration

	mu       sync.RWMutex
	handlers map[string]Handler
	// observer sees every served request after its response has been written.
	observer func(*Request)

	wg       sync.WaitGroup
	stopOnce sync.Once
	stopped  chan struct{}
}

// NewServer builds a server over an already-bound listener.
func NewServer(listener Listener, logger *slog.Logger, requestTimeout time.Duration) *Server {
	if requestTimeout <= 0 {
		requestTimeout = RequestTimeout
	}
	return &Server{
		listener: listener,
		logger:   logger,
		timeout:  requestTimeout,
		handlers: make(map[string]Handler),
		stopped:  make(chan struct{}),
	}
}

// Handle registers a method handler.
func (s *Server) Handle(method string, handler Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

// Observe registers a callback that sees every request whose response has been
// written. It is for bookkeeping the caller is not waiting on — the daemon uses
// it to notice a newer client — so it runs after the answer has gone out and
// its outcome can never affect one.
func (s *Server) Observe(observer func(*Request)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = observer
}

// Methods lists the registered method names.
func (s *Server) Methods() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.handlers))
	for method := range s.handlers {
		out = append(out, method)
	}
	return out
}

// Endpoint is the address the server listens on.
func (s *Server) Endpoint() string { return s.listener.Endpoint() }

// Serve accepts connections until Stop is called or the context is canceled.
// Each connection is handled in its own goroutine (§9.3 step 5).
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		select {
		case <-ctx.Done():
			s.Stop()
		case <-s.stopped:
		}
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopped:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("ipc: accept: %w", err)
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(ctx, conn)
		}()
	}
}

// Stop closes the listener; in-flight requests are not interrupted.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		_ = s.listener.Close()
	})
}

// Shutdown stops accepting and waits for in-flight requests, up to the
// context deadline (§9.3 step 6: finish in-flight requests within 2 s).
func (s *Server) Shutdown(ctx context.Context) error {
	s.Stop()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(s.timeout + time.Second)); err != nil {
		s.logf("set connection deadline", err)
		return
	}

	framer := NewFramer(conn)
	var request Request
	if err := framer.Read(&request); err != nil {
		if errors.Is(err, io.EOF) {
			return
		}
		// A malformed request still gets a well-formed answer.
		_ = framer.Write(NewErrorResponse("", CodeBadRequest, err.Error()))
		return
	}

	response := s.dispatch(ctx, &request)
	if err := framer.Write(response); err != nil {
		s.logf("write response", err)
	}
	s.observe(&request)
}

// observe runs the registered observer, guarding it so a mistake there cannot
// take down a connection goroutine.
func (s *Server) observe(request *Request) {
	s.mu.RLock()
	observer := s.observer
	s.mu.RUnlock()
	if observer == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logf("request observer", fmt.Errorf("panic: %v", recovered))
		}
	}()
	observer(request)
}

func (s *Server) dispatch(ctx context.Context, request *Request) *Response {
	if !SupportedProtocol(request.ProtocolVersion) {
		return NewErrorResponse(request.RequestID, CodeUnsupportedProtocol,
			fmt.Sprintf("protocol version %d is not supported", request.ProtocolVersion))
	}

	s.mu.RLock()
	handler, ok := s.handlers[request.Method]
	s.mu.RUnlock()
	if !ok {
		return NewErrorResponse(request.RequestID, CodeBadRequest,
			fmt.Sprintf("unknown method %q", request.Method))
	}

	requestCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	result, err := s.invoke(requestCtx, handler, request)
	if err != nil {
		var protocolErr *Error
		if errors.As(err, &protocolErr) {
			return NewErrorResponse(request.RequestID, protocolErr.Code, protocolErr.Message)
		}
		s.logf("handler "+request.Method, err)
		return NewErrorResponse(request.RequestID, CodeInternal, err.Error())
	}

	response, err := NewResponse(request.RequestID, result)
	if err != nil {
		s.logf("encode result for "+request.Method, err)
		return NewErrorResponse(request.RequestID, CodeInternal, err.Error())
	}
	return response
}

// invoke runs a handler with panic recovery, so a bug in one handler can never
// take the daemon down or silently drop a request (§26, I-12).
func (s *Server) invoke(ctx context.Context, handler Handler, request *Request) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic in %s handler: %v", request.Method, recovered)
		}
	}()
	return handler(ctx, request)
}

func (s *Server) logf(message string, err error) {
	if s.logger == nil {
		return
	}
	s.logger.Error("ipc: "+message, "error", err)
}
