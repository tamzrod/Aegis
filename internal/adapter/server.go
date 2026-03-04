// internal/adapter/server.go
package adapter

import (
	"log"
	"net"
	"sync"

	"github.com/tamzrod/Aegis/internal/core"
)

// Server is a Modbus TCP server adapter.
// It accepts connections and dispatches each to HandleConn.
// The server reads and writes the shared in-process Store.
// It is a pure transport adapter: no logic, no state, no interpretation.
type Server struct {
	listen    string
	store     core.Store
	authority Enforcer

	mu   sync.Mutex
	ln   net.Listener
	done chan struct{}
}

// NewServer creates a Server for the given listen address, store, and authority enforcer.
func NewServer(listen string, store core.Store, authority Enforcer) *Server {
	return &Server{
		listen:    listen,
		store:     store,
		authority: authority,
		done:      make(chan struct{}),
	}
}

// NewServerWithListener creates a Server with a pre-bound net.Listener.
// Using a pre-bound listener ensures Shutdown can always close it immediately,
// eliminating the race between goroutine scheduling and Shutdown seeing a nil ln.
func NewServerWithListener(listen string, ln net.Listener, store core.Store, authority Enforcer) *Server {
	return &Server{
		listen:    listen,
		store:     store,
		authority: authority,
		ln:        ln,
		done:      make(chan struct{}),
	}
}

// Addr returns the listen address string for this server.
func (s *Server) Addr() string {
	return s.listen
}

// ListenAndServe starts accepting Modbus TCP connections.
// Each connection is handled in its own goroutine.
// This function blocks until the listener fails or Shutdown is called.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.ln = nil
		s.mu.Unlock()
		close(s.done)
	}()

	log.Printf("adapter: modbus tcp listening on %s", s.listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go HandleConn(conn, s.store, s.authority)
	}
}

// Shutdown closes the listener and waits for ListenAndServe to return.
// It is safe to call Shutdown if ListenAndServe was never started.
func (s *Server) Shutdown() {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
		<-s.done
	}
}

// Serve starts accepting Modbus TCP connections on the pre-bound listener
// stored in s.ln (set by NewServerWithListener).
// It blocks until the listener is closed or returns an error.
func (s *Server) Serve() error {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.ln = nil
		s.mu.Unlock()
		close(s.done)
	}()

	log.Printf("adapter: modbus tcp listening on %s", s.listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go HandleConn(conn, s.store, s.authority)
	}
}
