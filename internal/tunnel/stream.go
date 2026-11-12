package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ErrStreamGone es el error tipado de un stream que no sobrevive a la
// caida del peer: el Watcher re-abre la conexion, y cada stream o se
// re-anuda (Registry) o muere con este error — nunca queda colgado
// (T-0401 AC3, "0 hangs").
var ErrStreamGone = errors.New("tunnel: stream gone (peer lost)")

// ErrSessionClosed: la sesion murio (readLoop salio) sin causa de stream.
var ErrSessionClosed = errors.New("tunnel: session closed")

// chunk es un data-frame encolado por stream. El payload se copia porque
// el buffer del FrameReader se reutiliza.
type chunk struct {
	data []byte
	eof  bool
}

// Stream es un stream logico del tunel: cada sesion SOCKS5 del gateway es
// uno (BLUEPRINT §7). Escrituras por chunk Data; lecturas desde la cola.
type Stream struct {
	id     uint32
	s      *Session
	q      chan chunk
	rbuf   []byte
	eof    bool
	mu     sync.Mutex
	closed bool
	rerr   error
}

// ID es el identificador de stream (impares: iniciados por el agente).
func (s *Stream) ID() uint32 { return s.id }

// Write parte p en chunks Data (MaxFramePayload): un stream no encola por
// detras de otro en el write path mas alla del lock de la conexion.
func (s *Stream) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > MaxFramePayload {
			n = MaxFramePayload
		}
		if err := s.s.writeFrame(Header{Kind: KindData, Stream: s.id}, p[:n]); err != nil {
			return total, err
		}
		p = p[n:]
		total += n
	}
	return total, nil
}

// Read entrega bytes del peer; io.EOF tras CloseWrite remoto.
func (s *Stream) Read(p []byte) (int, error) {
	for {
		if len(s.rbuf) > 0 {
			n := copy(p, s.rbuf)
			s.rbuf = s.rbuf[n:]
			return n, nil
		}
		if s.eof {
			return 0, io.EOF
		}
		c, ok := <-s.q
		if !ok {
			if s.rerr != nil {
				return 0, s.rerr
			}
			return 0, io.EOF
		}
		if c.eof {
			s.eof = true
			continue
		}
		s.rbuf = c.data
	}
}

// CloseWrite señala half-close; Close cierra la lectura local también.
func (s *Stream) CloseWrite() error {
	return s.s.writeFrame(Header{Kind: KindClose, Stream: s.id}, nil)
}

func (s *Stream) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if len(s.q) > 0 {
			// drain: no bloquear al writer remoto con un lector muerto
			for len(s.q) > 0 {
				<-s.q
			}
		}
	}
	s.mu.Unlock()
	return s.CloseWrite()
}

// Session multiplexa streams sobre UNA conexion (T-0401 AC1: 50 streams,
// 10 MB eco, una sola conexion ESTAB). El readLoop es el unico lector:
// valida el CRC por frame y despacha a la cola del stream destino.
type Session struct {
	Conn net.Conn

	rd     *FrameReader
	wmu    sync.Mutex
	mu     sync.Mutex
	conns  map[uint32]*Stream
	nextID uint32
	accept chan *Stream
	closed bool
	serr   error

	Corrupt atomic.Uint64 // frames con CRC invalido en el receptor (AC1: 0)
	done    chan struct{}
	once    sync.Once
}

// NewSession arranca el readLoop sobre conn (ya TLS con ALPN "h2", ver
// tls.go): el framing no asume nada del transporte bajo la conexion.
func NewSession(conn net.Conn) *Session {
	s := &Session{
		Conn:   conn,
		rd:     NewFrameReader(conn, MaxFramePayload),
		conns:  map[uint32]*Stream{},
		accept: make(chan *Stream, 16),
		done:   make(chan struct{}),
		nextID: 1,
	}
	go s.readLoop()
	return s
}

// AcceptStream devuelve streams iniciados por el peer. El gateway abre
// sesiones de cliente por esta via.
func (s *Session) AcceptStream() (*Stream, error) {
	select {
	case st := <-s.accept:
		if st == nil {
			return nil, s.shutdownErr()
		}
		return st, nil
	case <-s.done:
		return nil, s.shutdownErr()
	}
}

// Open abre un stream local (id impar) y anuncia KindOpen.
func (s *Session) Open() (*Stream, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, s.shutdownErr()
	}
	id := s.nextID
	s.nextID += 2
	st := s.newStreamLocked(id)
	s.mu.Unlock()
	if err := s.writeFrame(Header{Kind: KindOpen, Stream: id}, nil); err != nil {
		s.dropStream(id)
		return nil, err
	}
	return st, nil
}

// OpenWith abre un stream con id explicito — lo usa Registry para
// re-anudar el mismo stream logico tras la reconexion (AC3).
func (s *Session) OpenWith(id uint32) (*Stream, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, s.shutdownErr()
	}
	if _, dup := s.conns[id]; dup {
		s.mu.Unlock()
		return nil, fmt.Errorf("tunnel: stream %d ya activo", id)
	}
	if id >= s.nextID {
		s.nextID = id + 2
	}
	st := s.newStreamLocked(id)
	s.mu.Unlock()
	if err := s.writeFrame(Header{Kind: KindOpen, Stream: id}, nil); err != nil {
		s.dropStream(id)
		return nil, err
	}
	return st, nil
}

// Done se cierra cuando la sesion muere (peer caido o Close local).
func (s *Session) Done() <-chan struct{} { return s.done }

// Err es la causa de cierre (nil si sigue viva).
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serr
}

// Close cierra la conexion: el readLoop propaga ErrSessionClosed a todos
// los streams (sin hangs: las colas se cierran).
func (s *Session) Close() error {
	var err error
	s.once.Do(func() {
		err = s.Conn.Close()
		s.shutdown(ErrSessionClosed)
	})
	return err
}

func (s *Session) newStreamLocked(id uint32) *Stream {
	st := &Stream{id: id, s: s, q: make(chan chunk, 8)}
	s.conns[id] = st
	return st
}

func (s *Session) dropStream(id uint32) {
	s.mu.Lock()
	delete(s.conns, id)
	s.mu.Unlock()
}

func (s *Session) writeFrame(h Header, payload []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.Conn == nil {
		return ErrSessionClosed
	}
	return WriteFrame(s.Conn, h, payload)
}

// ServeEcho es el peer de prueba (gateway simulado): acepta streams y
// devuelve cada data-frame al emisor. Regresa al cerrar la sesion.
func (s *Session) ServeEcho() error {
	for {
		select {
		case <-s.done:
			return s.shutdownErr()
		case st := <-s.accept:
			if st == nil {
				return s.shutdownErr()
			}
			go func(st *Stream) {
				for {
					c, ok := <-st.q
					if !ok || c.eof {
						return
					}
					if err := s.writeFrame(Header{Kind: KindData, Stream: st.id}, c.data); err != nil {
						return
					}
				}
			}(st)
		}
	}
}

func (s *Session) readLoop() {
	for {
		h, payload, err := s.rd.ReadFrame()
		if err != nil {
			if errors.Is(err, ErrFrameChecksum) {
				// Integridad: el frame corrupto se descarta y la conexion
				// sigue; el contador es el assert del criterio AC1.
				s.Corrupt.Add(1)
				continue
			}
			s.shutdown(err)
			return
		}
		data := append([]byte(nil), payload...) // el buffer se reutiliza
		switch h.Kind {
		case KindOpen:
			st := s.peerStream(h.Stream)
			select {
			case s.accept <- st:
			case <-s.done:
				return
			}
		case KindData:
			st := s.lookup(h.Stream)
			if st == nil {
				continue
			}
			st.deliver(chunk{data: data})
		case KindClose:
			st := s.lookup(h.Stream)
			if st == nil {
				continue
			}
			st.deliver(chunk{eof: true})
		case KindGoAway:
			s.shutdown(ErrSessionClosed)
			return
		case KindPing, KindHeartbeat, KindCmdAck:
			// el plano de control lo consume el bucle del agente; el mux
			// los drena para no cegar el readLoop.
			continue
		}
	}
}

func (s *Session) lookup(id uint32) *Stream {
	s.mu.Lock()
	st := s.conns[id]
	s.mu.Unlock()
	return st
}

func (s *Session) peerStream(id uint32) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.conns[id]; ok {
		return st
	}
	st := &Stream{id: id, s: s, q: make(chan chunk, 8)}
	s.conns[id] = st
	return st
}

func (st *Stream) deliver(c chunk) {
	select {
	case st.q <- c:
	case <-st.s.done:
	}
}

func (s *Session) shutdown(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.serr == nil {
		s.serr = err
	}
	streams := make([]*Stream, 0, len(s.conns))
	for id, st := range s.conns {
		streams = append(streams, st)
		delete(s.conns, id)
	}
	s.mu.Unlock()
	for _, st := range streams {
		st.mu.Lock()
		if !st.closed {
			st.closed = true
			st.rerr = s.serr
		}
		st.mu.Unlock()
		close(st.q)
	}
	close(s.accept)
	close(s.done)
}

func (s *Session) shutdownErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serr != nil {
		return s.serr
	}
	return ErrSessionClosed
}

// Watcher reabre la conexion cuando el peer muere a mitad de stream:
// redial con backoff (T-0406) hasta que OnSession devuelve nil o el
// proceso muere. AC3 T-0401: reconexion en < 5 s con backoff ± jitter.
type Watcher struct {
	Dial      func() (net.Conn, error)
	Backoff   *Backoff
	OnSession func(*Session) error // bloquea hasta que la sesion muere
	Cap       time.Duration        // techo por re-intento (0 = solo backoff)
}

func (w *Watcher) Run() error {
	if w.Dial == nil || w.OnSession == nil {
		return errors.New("tunnel: Watcher sin Dial/OnSession")
	}
	for {
		conn, err := w.Dial()
		if err == nil {
			w.Backoff.Reset()
			sess := NewSession(conn)
			lastErr := w.OnSession(sess)
			_ = sess.Close()
			if lastErr == nil {
				return nil
			}
		}
		d := w.Backoff.Next()
		if w.Cap > 0 && d > w.Cap {
			d = w.Cap
		}
		time.Sleep(d)
	}
}
