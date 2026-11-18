//go:build socks5

package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// SOCKS5 RFC 1928 + auth de usuario/clave RFC 1929: el username lleva el
// bot-session del gateway y el node_secret lo verifica en el propio nodo
// (T-0602). El listener nace en loopback y el nodo nunca escucha fuera
// (CGNAT por construccion, T-0601 AC1).

const (
	socksVersion   = 0x05
	s5MethodUser   = 0x02
	s5AuthVersion  = 0x01 // RFC 1929
	s5AuthReject   = 0x01 // RFC 1929: auth fallida
	s5CmdConnect   = 0x01
	s5ATypIPv4     = 0x01
	s5ATypDomain   = 0x03
	s5ATypIPv6     = 0x04
	s5RepOK        = 0x00
	s5RepFailed    = 0x01
	s5RepNotReach  = 0x04
	s5RepCmdUnsupp = 0x07
	s5RepATypUnsup = 0x08
)

// errAuthForged marca el camino "stream cerrado al instante con 0 bytes"
// del token forjado (T-0602 AC2): la apertura conto, el relay no corre.
var errAuthForged = errors.New("proxy: token forjado: stream cerrado sin bytes")

// session es el estado por conexion SOCKS: un CONNECT = una sesion = un
// stream H2. Las ranuras adquiridas (tope tecnico y admision) se
// devuelven exactamente una vez al terminar.
type session struct {
	id       SessionIdentity
	token    string
	host     string
	port     int
	stream   io.ReadWriteCloser
	acquired bool // slot de la StreamTable tomado
	admitted bool // slot de la Admission tomado
}

// Server es el proxy SOCKS5 embebido del agente.
type Server struct {
	cfg      Config
	ln       net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	table    *StreamTable
	log      *slog.Logger
	streams  atomic.Int64 // streams H2 abiertos por el proxy
	declined atomic.Int64 // CONNECT rechazados (auth/topes)
}

// Start valida el config, enlaza el listener loopback y lanza el bucle
// accept en goroutine. Sin tunel aun no rompe nada: cada CONNECT recibe
// "host unreachable" interno y el listener sigue vivo.
func Start(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	addr := cfg.Addr
	if addr == "" {
		addr = DefaultSOCKSAddr
	}
	ln, err := listenTCP(addr)
	if err != nil {
		return nil, err
	}
	table := cfg.Table
	if table == nil {
		table = NewStreamTable(MaxNominalStreams)
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, ln: ln, ctx: ctx, cancel: cancel, table: table, log: log}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Addr devuelve la direccion real del listener (efemera en tests).
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Loopback confirma que el socket escuchado es loopback: el doble del
// assert "ss -ltn no lista ningun puerto externo" (T-0601 AC1).
func (s *Server) Loopback() bool {
	if ta, ok := s.ln.Addr().(*net.TCPAddr); ok {
		return ta.IP.IsLoopback()
	}
	return false
}

// StreamsOpen devuelve los streams H2 que el proxy ha abierto.
func (s *Server) StreamsOpen() int64 { return s.streams.Load() }

// Declined devuelve los CONNECT rechazados (auth/topes) para asserts.
func (s *Server) Declined() int64 { return s.declined.Load() }

// Close cierra el listener y cancela el contexto del bucle.
func (s *Server) Close() error {
	err := s.ln.Close()
	s.cancel()
	return err
}

// Wait espera a que el bucle accept y los relays terminen (tests).
func (s *Server) Wait() { s.wg.Wait() }

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Temporary() {
				continue
			}
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveConn(conn)
		}()
	}
}

// serveConn atiende un unico CONNECT por conexion: el gateway abre una
// conexion loopback por sesion; el SOCKS5 interno no traspasa auth entre
// sesiones (cada conexion lleva su propio bot-session, T-0607 AC2).
func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	defer func() {
		if r := recover(); r != nil {
			if s.cfg.Sup != nil && s.cfg.Sup.RegisterPanic() {
				s.log.Warn("proxy: modo solo-control tras panico", "panic", r)
			} else {
				s.log.Error("proxy: panic en stream", "panic", r)
			}
		}
	}()
	ses, err := s.handshake(conn)
	switch {
	case err == nil:
		s.relay(conn, ses)
	case errors.Is(err, errAuthForged):
		s.finish(ses)
	default:
		s.declined.Add(1)
		s.log.Info("proxy: handshake rechazado", "err", err,
			"redis_calls", 0,
			"status_401", errors.Is(err, ErrTokenExpired))
	}
}

// handshake ejecuta greeting + RFC1929 + CONNECT y abre el stream. Las
// bot-session malformado o caducado se rechaza antes de tocar el nodo.
// causas de rechazo del bot-session son tipadas:
//   - usuario malformado o ts > 30 s (401 interno): rechazados aqui, el
//     contador de streams H2 no sube (T-0601 AC3, T-0602 AC2).
//   - HMAC forjado: el stream se abre y se cierra al instante con 0
//     bytes (errAuthForged).
func (s *Server) handshake(conn net.Conn) (*session, error) {
	hs := bufio.NewWriterSize(conn, 8192) // corriente handshake→cliente
	r := bufio.NewReaderSize(conn, 512)

	ver, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if ver != socksVersion {
		return nil, ErrTokenMalformed
	}
	nm, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	methods := make([]byte, nm)
	if _, err := io.ReadFull(r, methods); err != nil {
		return nil, err
	}
	hasUser := false
	for _, m := range methods {
		if m == s5MethodUser {
			hasUser = true
			break
		}
	}
	if !hasUser {
		return nil, errors.New("proxy: cliente SOCKS sin metodo user/pass")
	}
	if _, err := hs.Write([]byte{socksVersion, s5MethodUser}); err != nil {
		return nil, err
	}
	if err := hs.Flush(); err != nil {
		return nil, err
	}

	uver, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if uver != s5AuthVersion {
		return nil, errors.New("proxy: version auth RFC1929 != 1")
	}
	uname, err := readLengthed(r, 255)
	if err != nil {
		return nil, err
	}
	if _, err := readLengthed(r, 255); err != nil { // password opaco: lo valida el gateway
		return nil, err
	}
	token := string(uname)
	id, verr := VerifyToken(s.cfg.Secret, token, time.Now())
	if verr != nil && !errors.Is(verr, ErrTokenForged) {
		// malformed o expired (401 interno): rechazo sin abrir stream.
		_, _ = conn.Write([]byte{s5AuthVersion, s5AuthReject})
		return nil, verr
	}
	// RFC1929 §3: subnegociacion completada — VER=1, STATUS=0.
	if _, err := conn.Write([]byte{s5AuthVersion, 0x00}); err != nil {
		return nil, err
	}

	// CONNECT
	ver2, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if ver2 != socksVersion {
		return nil, errors.New("proxy: version request != 5")
	}
	cmd, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if _, err := r.ReadByte(); err != nil { // RSV
		return nil, err
	}
	if cmd != s5CmdConnect {
		s.writeReply(hs, s5RepCmdUnsupp)
		return nil, errors.New("proxy: comando no soportado")
	}
	host, _, err := readAddress(r)
	if err != nil {
		if errors.Is(err, errATypUnsupp) {
			s.writeReply(hs, s5RepATypUnsup)
		}
		return nil, err
	}
	var pb [2]byte
	if _, err := io.ReadFull(r, pb[:]); err != nil {
		return nil, err
	}

	// Topes del nodo: modo solo-control, tope tecnico y admision (el
	// minimo de las tres capacidades manda, A1-17).
	if s.cfg.Sup != nil && s.cfg.Sup.ControlOnly() {
		s.writeReply(hs, s5RepFailed)
		return nil, errors.New("proxy: modo solo-control")
	}
	ses := &session{id: id, token: token, host: host, port: int(pb[0])<<8 | int(pb[1])}
	if !s.table.Acquire() {
		s.writeReply(hs, s5RepFailed)
		return nil, errors.New("proxy: tope tecnico de streams")
	}
	ses.acquired = true
	if s.cfg.Adm != nil {
		if dec := s.cfg.Adm.Admit(id.TenantID, s.cfg.TenantQuota, s.table.Open()); !dec.Admitted {
			s.table.Release()
			ses.acquired = false
			s.writeReply(hs, s5RepFailed)
			return nil, errors.New("proxy: admision rechazada: " + dec.Reason)
		}
		ses.admitted = true
	}

	meta := StreamMeta{
		ClientID: id.ClientID, TenantID: id.TenantID, SessionID: id.SessionID,
		NodeID: id.NodeID, TargetHost: host, TargetPort: uint16(ses.port),
		SessionToken: token,
	}
	stream, oerr := s.cfg.Opener.OpenStream(s.ctx, meta)
	if oerr != nil {
		s.releaseSlots(ses)
		s.writeReply(hs, s5RepNotReach)
		return nil, oerr
	}
	ses.stream = stream
	s.streams.Add(1)

	if errors.Is(verr, ErrTokenForged) {
		// Forjado: reply OK, stream cerrado al instante, 0 bytes.
		s.writeReply(hs, s5RepOK)
		_ = stream.Close()
		ses.stream = nil
		s.releaseSlots(ses)
		return ses, errAuthForged
	}
	s.writeReply(hs, s5RepOK)
	return ses, nil
}

// releaseSlots devuelve la ranura tecnica y la de admision tomadas.
func (s *Server) releaseSlots(ses *session) {
	if ses.admitted && s.cfg.Adm != nil {
		s.cfg.Adm.Leave(ses.id.TenantID)
		ses.admitted = false
	}
	if ses.acquired {
		s.table.Release()
		ses.acquired = false
	}
}

// readAddress interpreta ATYP y devuelve host. El filtro de destino LAN
// (A3-X2) lo aplica el gateway; aqui solo se propaga el destino.
func readAddress(r *bufio.Reader) (string, byte, error) {
	atyp, err := r.ReadByte()
	if err != nil {
		return "", 0, err
	}
	switch atyp {
	case s5ATypIPv4:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", atyp, err
		}
		return net.IP(b[:]).String(), atyp, nil
	case s5ATypDomain:
		n, err := r.ReadByte()
		if err != nil {
			return "", atyp, err
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", atyp, err
		}
		return string(b), atyp, nil
	case s5ATypIPv6:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", atyp, err
		}
		return net.IP(b[:]).String(), atyp, nil
	default:
		return "", atyp, errATypUnsupp
	}
}

// errATypUnsupp distingue el ATYP desconocido para responder 0x08.
var errATypUnsupp = errors.New("proxy: ATYP no soportado")

// relay mueve bytes en ambos sentidos con las corrientes fijas de 8 KiB
// (up: cliente→stream; down: stream→cliente) y contadores propios de la
// sesion. El bucket deforma el stream cerca de la fuente.
func (s *Server) relay(conn net.Conn, ses *session) {
	stream := ses.stream
	if stream == nil {
		return
	}
	ses.stream = nil
	s.register(ses)
	defer s.unregister(ses)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	// vigilante de cancelacion: al cerrar el servidor, las lecturas
	// bloqueadas se liberan por deadline (cierre limpio de botd).
	if d, ok := stream.(interface{ SetReadDeadline(time.Time) error }); ok {
		go func() {
			<-ctx.Done()
			past := time.Now().Add(time.Nanosecond)
			_ = conn.SetReadDeadline(past)
			_ = d.SetReadDeadline(past)
		}()
	} else {
		go func() {
			<-ctx.Done()
			_ = conn.SetReadDeadline(time.Now().Add(time.Nanosecond))
		}()
	}
	if s.cfg.Bucket != nil {
		stream = Shape(stream, s.cfg.Bucket, ctx)
	}
	up := bufio.NewWriterSize(stream, 8192) // corriente cliente→stream
	down := bufio.NewWriterSize(conn, 8192) // corriente stream→cliente
	drain := bufio.NewWriterSize(io.Discard, 8192)

	sess := ses.id.SessionID
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // corriente cliente→stream
		defer wg.Done()
		defer func() {
			cancel()
			// el cliente murio: cerrar el stream libera la lectura
			// pendiente de la otra corriente (fin de sesion).
			_ = stream.Close()
		}()
		buf := make([]byte, StreamBufSize)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				if s.cfg.CountersTable != nil {
					s.cfg.CountersTable.AddIn(sess, uint64(n))
				}
				if _, werr := up.Write(buf[:n]); werr != nil {
					return
				}
				if ferr := up.Flush(); ferr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()
	go func() { // corriente stream→cliente
		defer wg.Done()
		defer func() {
			cancel()
			_ = down.Flush()
			_ = conn.Close() // libera la lectura del cliente si sigue viva
		}()
		buf := make([]byte, StreamBufSize)
		for {
			n, rerr := stream.Read(buf)
			if n > 0 {
				if s.cfg.CountersTable != nil {
					s.cfg.CountersTable.AddOut(sess, uint64(n))
				}
				w := down
				if rerr != nil {
					w = drain // bytes llegados con el stream muriendo
				}
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				_ = w.Flush()
			}
			if rerr != nil {
				return
			}
		}
	}()
	wg.Wait()
	_, _ = drain.Write(nil)
	_ = drain.Flush()
	s.finish(ses)
}

// register alta la sesion en el registro (via GOAWAY por cuota) y en los
// contadores si el nodo los lleva.
func (s *Server) register(ses *session) {
	if s.cfg.Reg != nil {
		s.cfg.Reg.Register(&Session{
			SessionID: ses.id.SessionID, TenantID: ses.id.TenantID,
			OpenedAt: time.Now().UTC(), stream: ses.stream,
		})
	}
}

// unregister quita la sesion del registro al terminar el relay.
func (s *Server) unregister(ses *session) {
	if s.cfg.Reg != nil && ses.id.SessionID != "" {
		s.cfg.Reg.Unregister(ses.id.SessionID)
	}
}

// finish libera stream, contadores y ranuras de la sesion.
func (s *Server) finish(ses *session) {
	if ses.stream != nil {
		_ = ses.stream.Close()
		ses.stream = nil
	}
	if s.cfg.CountersTable != nil && ses.id.SessionID != "" {
		s.cfg.CountersTable.CloseSession(ses.id.SessionID)
	}
	s.releaseSlots(ses)
}

// writeReply emite la respuesta CONNECT (BND 0.0.0.0:0 — el bind real lo
// fija el gateway).
func (s *Server) writeReply(hs *bufio.Writer, rep byte) {
	_ = hs.WriteByte(socksVersion)
	_ = hs.WriteByte(rep)
	_ = hs.WriteByte(0x00)
	_ = hs.WriteByte(s5ATypIPv4)
	_ = hs.WriteByte(0)
	_ = hs.WriteByte(0)
	_ = hs.WriteByte(0)
	_ = hs.WriteByte(0)
	_ = hs.WriteByte(0)
	_ = hs.WriteByte(0)
	_ = hs.Flush()
}

func readLengthed(r *bufio.Reader, max int) ([]byte, error) {
	n, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if int(n) > max {
		return nil, errors.New("proxy: campo longitud excedida")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}
