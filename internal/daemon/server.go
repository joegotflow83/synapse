package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

// hotIndex is the daemon's in-memory mirror of index.cbor for a single ledger
// directory. It is updated after each insert and rebuilt after each compact,
// eliminating per-query disk reads for indexed operations.
type hotIndex struct {
	mu      sync.RWMutex
	idIdx   map[string]*ledger.IndexEntry
	typeIdx map[string][]int64
	// loaded is true once the index has been initialised from disk or
	// populated entirely from in-process inserts.
	loaded bool
}

// Server listens on a Unix socket and routes JSON requests to ledger operations.
//
// It maintains a per-directory ledger cache so repeated operations against the
// same directory reuse the same (warm) Ledger handle, eliminating per-call
// open/stat overhead. The main performance benefit is that the Go runtime
// stays alive between requests — no ~5.3ms startup per call.
//
// In addition, Server keeps a hot in-memory index per directory (hotIndex)
// updated on each insert and rebuilt on each compact. Query and Get use this
// hot index directly, eliminating index.cbor disk reads on the critical path.
type Server struct {
	socketPath string
	ledgers    map[string]*ledger.Ledger
	indexes    map[string]*hotIndex
	mu         sync.Mutex
	listener   net.Listener
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewServer creates a Server that will listen on socketPath.
func NewServer(socketPath string) *Server {
	return &Server{
		socketPath: socketPath,
		ledgers:    make(map[string]*ledger.Ledger),
		indexes:    make(map[string]*hotIndex),
		done:       make(chan struct{}),
	}
}

// Start begins listening on the Unix socket and serving requests.
// It blocks until Stop is called or a SIGTERM/SIGINT is received.
func (s *Server) Start() error {
	// Remove any stale socket file left by a previous (crashed) run.
	os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.socketPath, err)
	}
	s.listener = ln

	// Handle SIGTERM/SIGINT for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			s.Stop()
		case <-s.done:
		}
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				s.wg.Wait()
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// Stop gracefully shuts down the server: closes the listener, waits for
// in-flight requests to finish, and removes the socket file.
func (s *Server) Stop() {
	select {
	case <-s.done:
		return // already stopped
	default:
		close(s.done)
	}
	s.listener.Close()
	s.wg.Wait()
	os.Remove(s.socketPath)
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		writeResp(conn, ErrResponse("decode request: "+err.Error()))
		return
	}
	writeResp(conn, s.handle(&req))
}

func writeResp(conn net.Conn, resp *Response) {
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	conn.Write(data) //nolint:errcheck
}

// getLedger returns a cached Ledger handle for dir, opening it if needed.
func (s *Server) getLedger(dir string) (*ledger.Ledger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.ledgers[dir]; ok {
		return l, nil
	}
	l, err := ledger.Open(dir)
	if err != nil {
		return nil, err
	}
	s.ledgers[dir] = l
	return l, nil
}

// getOrCreateHotIndex returns the hotIndex for dir, creating an empty one if
// it does not exist yet. The hotIndex is lazily populated from disk on first
// use by ensureLoaded.
func (s *Server) getOrCreateHotIndex(dir string) *hotIndex {
	s.mu.Lock()
	defer s.mu.Unlock()
	hi, ok := s.indexes[dir]
	if !ok {
		hi = &hotIndex{
			idIdx:   make(map[string]*ledger.IndexEntry),
			typeIdx: make(map[string][]int64),
		}
		s.indexes[dir] = hi
	}
	return hi
}

// ensureLoaded loads index.cbor from disk into hi if it has not been loaded
// yet. Must be called with hi.mu held for writing.
func ensureLoaded(hi *hotIndex, dir string) {
	if hi.loaded {
		return
	}
	ip := filepath.Join(dir, "index.cbor")
	idIdx, _ := ledger.LoadIndex(ip)
	typeIdx, _ := ledger.LoadTypeIndex(ip)
	hi.idIdx = idIdx
	hi.typeIdx = typeIdx
	hi.loaded = true
}

// applyIndexEntries appends the new IndexEntries to the hot in-memory index.
// Must be called with hi.mu held for writing.
func applyIndexEntries(hi *hotIndex, ies []*ledger.IndexEntry) {
	for _, ie := range ies {
		hi.idIdx[ie.ID] = ie
		hi.typeIdx[ie.Type] = append(hi.typeIdx[ie.Type], ie.Offset)
	}
}

func (s *Server) handle(req *Request) *Response {
	switch req.Command {
	case CmdHealth:
		resp, _ := OKResponse(map[string]string{"status": "ok"})
		return resp

	case CmdShutdown:
		// Trigger shutdown asynchronously so we can still reply.
		go s.Stop()
		resp, _ := OKResponse(map[string]string{"status": "shutdown"})
		return resp
	}

	if req.Dir == "" {
		return ErrResponse("dir is required")
	}

	l, err := s.getLedger(req.Dir)
	if err != nil {
		return ErrResponse("open ledger: " + err.Error())
	}

	switch req.Command {
	case CmdInsert:
		return s.doInsert(l, req)
	case CmdInsertBatch:
		return s.doInsertBatch(l, req)
	case CmdQuery:
		return s.doQuery(l, req)
	case CmdQueryBatch:
		return s.doQueryBatch(l, req)
	case CmdGet:
		return s.doGet(l, req)
	case CmdCompact:
		return s.doCompact(l, req)
	case CmdListTypes:
		return s.doListTypes(l, req)
	case CmdCreateType:
		return s.doCreateType(l, req)
	case CmdReindex:
		return s.doReindex(l, req)
	default:
		return ErrResponse("unknown command: " + req.Command)
	}
}

func (s *Server) doInsert(l *ledger.Ledger, req *Request) *Response {
	var a InsertArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return ErrResponse("decode args: " + err.Error())
	}
	entry := &model.Entry{
		ID:            a.ID,
		Type:          a.Type,
		AgentID:       a.AgentID,
		Data:          a.Data,
		AgentMetadata: a.Metadata,
	}

	// Hold the hot-index write lock across the full ensureLoaded →
	// InsertIndexed → applyIndexEntries sequence to prevent concurrent
	// inserts from duplicating offsets in the typeIdx slice.
	hi := s.getOrCreateHotIndex(req.Dir)
	hi.mu.Lock()
	ensureLoaded(hi, req.Dir)
	ie, err := l.InsertIndexed(entry)
	if err == nil {
		applyIndexEntries(hi, []*ledger.IndexEntry{ie})
	}
	hi.mu.Unlock()
	if err != nil {
		return ErrResponse(err.Error())
	}

	resp, _ := OKResponse(map[string]string{"id": entry.ID})
	return resp
}

func (s *Server) doInsertBatch(l *ledger.Ledger, req *Request) *Response {
	var a InsertBatchArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return ErrResponse("decode args: " + err.Error())
	}
	entries := make([]*model.Entry, len(a.Entries))
	for i, e := range a.Entries {
		entries[i] = &model.Entry{
			ID:            e.ID,
			Type:          e.Type,
			AgentID:       e.AgentID,
			Data:          e.Data,
			AgentMetadata: e.Metadata,
		}
	}

	// Hold the hot-index write lock across the full ensureLoaded →
	// InsertBatchIndexed → applyIndexEntries sequence to prevent concurrent
	// inserts from duplicating offsets in the typeIdx slice.
	hi := s.getOrCreateHotIndex(req.Dir)
	hi.mu.Lock()
	ensureLoaded(hi, req.Dir)
	ies, err := l.InsertBatchIndexed(entries)
	if err == nil {
		applyIndexEntries(hi, ies)
	}
	hi.mu.Unlock()
	if err != nil {
		return ErrResponse(err.Error())
	}

	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	resp, _ := OKResponse(map[string][]string{"ids": ids})
	return resp
}

func (s *Server) doQuery(l *ledger.Ledger, req *Request) *Response {
	start := time.Now()

	var a QueryArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return ErrResponse("decode args: " + err.Error())
		}
	}

	// Build query options, injecting the hot type index when a type filter is
	// set so Query bypasses index.cbor disk reads.
	var scanned int
	opts := ledger.QueryOpts{Type: a.Type, Filter: a.Filter, Limit: a.Limit, Scanned: &scanned}
	if a.Type != "" {
		hi := s.getOrCreateHotIndex(req.Dir)
		hi.mu.RLock()
		if hi.loaded {
			// Make a shallow copy of the typeIdx slice for this type so the
			// lock can be released before the query executes.
			if offsets, ok := hi.typeIdx[a.Type]; ok {
				cp := make([]int64, len(offsets))
				copy(cp, offsets)
				opts.TypeIndex = map[string][]int64{a.Type: cp}
			} else if len(hi.idIdx) > 0 {
				// Index is loaded but type is absent — inject empty map so
				// Query returns nil without a disk read.
				opts.TypeIndex = map[string][]int64{}
			}
		}
		hi.mu.RUnlock()
	}

	results, err := l.Query(opts)
	if err != nil {
		return ErrResponse(err.Error())
	}
	if results == nil {
		results = []*model.Entry{}
	}
	stats := &ResponseStats{
		Scanned:    scanned,
		Matched:    len(results),
		DurationMs: float64(time.Since(start).Microseconds()) / 1000.0,
	}
	resp, _ := OKResponseWithStats(results, stats)
	return resp
}

func (s *Server) doQueryBatch(l *ledger.Ledger, req *Request) *Response {
	var specs []ledger.BatchQuerySpec
	if err := json.Unmarshal(req.Args, &specs); err != nil {
		return ErrResponse("decode args: " + err.Error())
	}

	// Pass the hot type index to avoid index.cbor disk reads.
	hi := s.getOrCreateHotIndex(req.Dir)
	hi.mu.RLock()
	var hotTypeIdx map[string][]int64
	if hi.loaded {
		// Deep-copy the type index so the lock can be released before the query.
		hotTypeIdx = make(map[string][]int64, len(hi.typeIdx))
		for t, offsets := range hi.typeIdx {
			cp := make([]int64, len(offsets))
			copy(cp, offsets)
			hotTypeIdx[t] = cp
		}
	}
	hi.mu.RUnlock()

	results, err := l.QueryBatchWithTypeIndex(specs, hotTypeIdx)
	if err != nil {
		return ErrResponse(err.Error())
	}
	resp, _ := OKResponse(results)
	return resp
}

func (s *Server) doGet(l *ledger.Ledger, req *Request) *Response {
	start := time.Now()

	var a GetArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return ErrResponse("decode args: " + err.Error())
	}

	// Pass the hot ID index to avoid index.cbor disk reads for non-history Get.
	var hotIDIdx map[string]*ledger.IndexEntry
	if !a.History {
		hi := s.getOrCreateHotIndex(req.Dir)
		hi.mu.RLock()
		if hi.loaded {
			// Shallow copy is safe: IndexEntry values are never mutated.
			hotIDIdx = make(map[string]*ledger.IndexEntry, len(hi.idIdx))
			for id, ie := range hi.idIdx {
				hotIDIdx[id] = ie
			}
		}
		hi.mu.RUnlock()
	}

	results, err := l.GetWithIDIndex(a.ID, a.History, hotIDIdx)
	if err != nil {
		return ErrResponse(err.Error())
	}

	elapsed := float64(time.Since(start).Microseconds()) / 1000.0

	// nil means not found — encode as special "not_found" status.
	if results == nil {
		return &Response{
			Status: "not_found",
			Stats:  &ResponseStats{Scanned: 0, Matched: 0, DurationMs: elapsed},
		}
	}

	stats := &ResponseStats{
		Scanned:    len(results),
		Matched:    len(results),
		DurationMs: elapsed,
	}
	resp, _ := OKResponseWithStats(results, stats)
	return resp
}

func (s *Server) doCompact(l *ledger.Ledger, req *Request) *Response {
	stats, err := l.Compact()
	if err != nil {
		return ErrResponse(err.Error())
	}
	// Invalidate cached ledger and hot index for this dir so the next
	// operation re-opens against the freshly compacted file and reloads the
	// rebuilt index.
	s.mu.Lock()
	delete(s.ledgers, req.Dir)
	delete(s.indexes, req.Dir)
	s.mu.Unlock()
	resp, _ := OKResponse(stats)
	return resp
}

func (s *Server) doListTypes(l *ledger.Ledger, _ *Request) *Response {
	types, err := l.ListTypes()
	if err != nil {
		return ErrResponse(err.Error())
	}
	resp, _ := OKResponse(types)
	return resp
}

func (s *Server) doReindex(l *ledger.Ledger, req *Request) *Response {
	n, err := l.Reindex()
	if err != nil {
		return ErrResponse(err.Error())
	}
	// Invalidate the hot index so subsequent operations reload from the
	// freshly rebuilt index.cbor.
	s.mu.Lock()
	delete(s.indexes, req.Dir)
	s.mu.Unlock()
	resp, _ := OKResponse(map[string]int{"entries_indexed": n})
	return resp
}

func (s *Server) doCreateType(l *ledger.Ledger, req *Request) *Response {
	var a CreateTypeArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return ErrResponse("decode args: " + err.Error())
	}
	if err := l.CreateType(a.Name, a.Description, a.Example); err != nil {
		return ErrResponse(err.Error())
	}
	resp, _ := OKResponse(map[string]string{"status": "ok"})
	return resp
}
