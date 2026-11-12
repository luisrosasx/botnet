// Package proxy es el plano de datos del nodo (BLUEPRINT §7): SOCKS5 en el
// loopback del agente, cada sesion del gateway es un stream H2 de la
// conexion saliente existente, y el nodo nunca escucha hacia afuera
// (CGNAT por construccion). La auth bot-session se valida localmente con
// el node_secret (sin RTT a Redis), los buffers son fijos de 8 KiB, el
// token bucket de 2-5 Mbps cerca de la fuente y los topes de admision
// (<=8 tenants, 150 streams nominales) los ejecuta el nodo.
//
// El transporte real (tunel TLS/H2) lo aporta internal/tunnel (EP-04);
// este paquete programa contra la seam StreamOpener declarada aqui.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
)

// ErrNoTunnel: el tunel H2 aun no puede abrir streams (EP-04 en
// construccion). El SOCKS5 lo propaga como "host unreachable" interno sin
// romper el listener ni el plano de control.
var ErrNoTunnel = errors.New("proxy: tunel H2 no disponible")

// ErrDisabled: el binario se compilo sin el tag socks5: no hay proxy
// embebido (el plano de control sigue vivo). cmd/botd lo loguea y sigue.
var ErrDisabled = errors.New("proxy: SOCKS5 deshabilitado en este build (tag socks5 ausente)")

// ErrNotLoopback: el proxy solo puede nacer en loopback — el nodo nunca
// escucha hacia afuera (CGNAT por construccion, T-0601 AC1).
var ErrNotLoopback = errors.New("proxy: el SOCKS5 del agente solo escucha en loopback")

// DefaultSOCKSAddr es la direccion embebida del proxy en el nodo (§7).
const DefaultSOCKSAddr = "127.0.0.1:1080"

// Config es el arranque del proxy embebido. Addr debe ser loopback; el
// Secret es el node_secret de 32 B del alta; Opener es la seam con el
// tunel H2. Los demas son topes opcionales: si son nil, el servidor
// arranca con los defaults del blueprint.
type Config struct {
	Addr          string // vacio → DefaultSOCKSAddr
	Secret        []byte // node_secret (32 B)
	Opener        StreamOpener
	Log           *slog.Logger
	Bucket        *Bucket       // token bucket 2-5 Mbps (nil = sin shaping)
	Table         *StreamTable  // tope tecnico de streams (nil = 150)
	Sup           *PanicBudget  // supervisor de streams (nil = sin modo control-only)
	Adm           *Admission    // admision de tenants (nil = sin tope de tenants)
	TenantQuota   int           // cuota concurrente por tenant (0 = sin tope)
	Reg           *Registry     // sesiones vivas para el GOAWAY por cuota
	CountersTable *CounterTable // contadores por sesion (nil = sin contadores)
}

// Validate comprueba lo minimo del arranque: secreto de 32 B, opener y
// loopback estricto.
func (c Config) Validate() error {
	if len(c.Secret) != 32 {
		return fmt.Errorf("proxy: node_secret de %d B != 32", len(c.Secret))
	}
	if c.Opener == nil {
		return errors.New("proxy: StreamOpener requerido")
	}
	addr := c.Addr
	if addr == "" {
		addr = DefaultSOCKSAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("proxy: addr %q: %w", c.Addr, err)
	}
	if host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("%w: %s", ErrNotLoopback, host)
	}
	return nil
}

// StreamMeta describe el stream que el nodo pide al tunel abrir: las
// cuatro piezas del bot-session verificadas, el destino SOCKS y el token
// opaco que el gateway relee en su plano.
type StreamMeta struct {
	ClientID     string
	TenantID     string
	SessionID    string // uuid de sesion del gateway
	NodeID       string
	TargetHost   string // destino CONNECT del cliente SOCKS
	TargetPort   uint16
	SessionToken string // bot-session ya verificado localmente
}

// Target returns host:port del destino, forma canonica para dialers.
func (m StreamMeta) Target() string {
	return m.TargetHost + ":" + itoa(int(m.TargetPort))
}

// StreamOpener es la seam con internal/tunnel: el tunel abre un stream H2
// por sesion sobre las hasta 4 conexiones salientes (GOAWAY_SESSION por
// id). La epica vecina se adapta a esta firma; el doble FakeOpener cubre
// los tests.
type StreamOpener interface {
	OpenStream(ctx context.Context, meta StreamMeta) (io.ReadWriteCloser, error)
}

// OpenerFunc adapta una closure a StreamOpener.
type OpenerFunc func(ctx context.Context, meta StreamMeta) (io.ReadWriteCloser, error)

func (f OpenerFunc) OpenStream(ctx context.Context, meta StreamMeta) (io.ReadWriteCloser, error) {
	return f(ctx, meta)
}

// NewOpenerFromSession adapta el *tunnel.Session.Open() de la epica
// paralela (stream.go: `Open() (*Stream, error)`, sin ctx ni meta) a la
// seam del proxy: *tunnel.Stream cumple io.ReadWriteCloser (Read/Write/
// Close). Uso real:
//
//	opener := proxy.NewOpenerFromSession(func() (io.ReadWriteCloser, error) {
//	    return sess.Open()
//	})
//
// La meta por sesion (bot-session, destino) viaja despues en el stream:
// el tunel la lleva en el OPEN_SESSION y aqui solo se abre el pipe.
func NewOpenerFromSession(open func() (io.ReadWriteCloser, error)) StreamOpener {
	return OpenerFunc(func(ctx context.Context, meta StreamMeta) (io.ReadWriteCloser, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return open()
	})
}

// FakeOpener es el doble de la seam: cada stream es un par de tubos que un
// echo goroutine devuelve. Contabiliza aperturas para los asserts de
// T-0601 (un usuario malformado no debe subir el contador).
type FakeOpener struct {
	mu        sync.Mutex
	streams   map[string]io.ReadWriteCloser
	Opened    atomic.Int64
	Closed    atomic.Int64
	BytesSeen atomic.Int64
	Err       error // si esta fijado, OpenStream lo devuelve
	// Echo fija el comportamiento del "gateway" del doble.
	Echo bool
}

func (f *FakeOpener) OpenStream(ctx context.Context, meta StreamMeta) (io.ReadWriteCloser, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.Opened.Add(1)
	c := newEchoPipe(f, f.Echo)
	if f.Echo {
		go func() { _, _ = io.Copy(c.pwOut, c.prIn) }()
	}
	f.mu.Lock()
	if f.streams == nil {
		f.streams = make(map[string]io.ReadWriteCloser)
	}
	f.streams[meta.SessionID] = c
	f.mu.Unlock()
	return c, nil
}

// CloseSession cierra el stream abierto de una sesion (via GOAWAY del
// doble). Devuelve false si no existe.
func (f *FakeOpener) CloseSession(sessionID string) bool {
	f.mu.Lock()
	c := f.streams[sessionID]
	delete(f.streams, sessionID)
	f.mu.Unlock()
	if c == nil {
		return false
	}
	_ = c.Close()
	return true
}

func (f *FakeOpener) forget(id string) {
	f.mu.Lock()
	delete(f.streams, id)
	f.mu.Unlock()
}

// echoPipe es el stream del doble: dos io.Pipe separan sentidos (nodo→
// "gateway" y "gateway"→nodo) para que el eco no se robe bytes con el
// lector del nodo. Close cierra ambos lados idempotente.
type echoPipe struct {
	w     *io.PipeWriter // lo que el nodo escribe
	r     *io.PipeReader // lo que el nodo lee
	pwOut *io.PipeWriter // lo que el eco devuelve al nodo
	prIn  *io.PipeReader // lo que el eco recibe del nodo
	once  sync.Once
	fn    *FakeOpener
	echo  bool
}

func newEchoPipe(f *FakeOpener, echo bool) *echoPipe {
	prIn, pwIn := io.Pipe()
	prOut, pwOut := io.Pipe()
	return &echoPipe{w: pwIn, r: prOut, pwOut: pwOut, prIn: prIn, fn: f, echo: echo}
}

func (e *echoPipe) Read(p []byte) (int, error) { return e.r.Read(p) }
func (e *echoPipe) Write(p []byte) (int, error) {
	if e.fn != nil {
		e.fn.BytesSeen.Add(int64(len(p)))
	}
	return e.w.Write(p)
}
func (e *echoPipe) Close() error {
	var err error
	e.once.Do(func() {
		_ = e.w.Close()
		err = e.pwOut.Close()
	})
	return err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [6]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
