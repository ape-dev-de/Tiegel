package server

// OpenConns returns the number of tracked connections (tests).
func (s *Server) OpenConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}
