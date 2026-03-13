package socket

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// ProcessContext is the context sent by framework integrations (Laravel, Symfony, etc.)
// Only PID and StartedAt are required. Meta can contain anything the module wants to report.
type ProcessContext struct {
	PID       int            `json:"pid"`
	StartedAt time.Time      `json:"started_at"`
	Meta      map[string]any `json:"meta"`
}

// Store is a thread-safe map of PID -> ProcessContext.
type Store struct {
	mu   sync.RWMutex
	data map[int]ProcessContext
}

func NewStore() *Store {
	return &Store{data: make(map[int]ProcessContext)}
}

func (s *Store) Set(ctx ProcessContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[ctx.PID] = ctx
}

func (s *Store) Get(pid int) (ProcessContext, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx, ok := s.data[pid]
	return ctx, ok
}

func (s *Store) Delete(pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, pid)
}

// Server listens on a Unix socket and accepts ProcessContext from framework modules.
type Server struct {
	path  string
	store *Store
}

func NewServer(socketPath string, store *Store) *Server {
	return &Server{path: socketPath, store: store}
}

func (s *Server) Listen() error {
	os.Remove(s.path)

	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	defer ln.Close()

	log.Printf("socket listening on %s", s.path)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	var ctx ProcessContext
	if err := json.NewDecoder(conn).Decode(&ctx); err != nil {
		return
	}

	s.store.Set(ctx)
}
