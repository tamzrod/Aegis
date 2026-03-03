// internal/adapter/server.go
package adapter

import (
	"log"
	"net"

	"github.com/tamzrod/Aegis/internal/core"
)

// Server is a Modbus TCP server adapter.
// It accepts connections and dispatches each to HandleConn.
// The server reads and writes the shared in-process Store.
// It is a pure transport adapter: no logic, no state, no interpretation.
type Server struct {
	listen    string
	store     core.Store
	authority *AuthorityRegistry
}

// NewServer creates a Server for the given listen address, store, and authority registry.
func NewServer(listen string, store core.Store, authority *AuthorityRegistry) *Server {
	return &Server{listen: listen, store: store, authority: authority}
}

// ListenAndServe starts accepting Modbus TCP connections.
// Each connection is handled in its own goroutine.
// This function blocks until the listener fails.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	defer ln.Close()

	log.Printf("adapter: modbus tcp listening on %s", s.listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go HandleConn(conn, s.store, s.authority)
	}
}
