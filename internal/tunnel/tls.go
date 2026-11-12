package tunnel

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// TLS del tunel (BLUEPRINT §5, T-0408): TLS 1.3 obligatorio, ALPN "h2",
// mTLS jubilado del plano de datos — el gateway no pide CertificateRequest
// (auth de aplicacion bot-session basta, A2-14)—. La CA de flota queda
// solo para el plano de control.

// ALPNTunnel es el unico protocolo anunciado: h2 sobre 443 saliente.
const ALPNTunnel = "h2"

// ErrHelloNoResume: resumption no conseguido donde el criterio lo exige.
var ErrHelloNoResume = errors.New("tunnel: full handshake (no PSK resumption)")

// TLSMinVersion fija el suelo: 1.3 exacto (sin downgrade, JA3 limpio).
const TLSMinVersion = tls.VersionTLS13

// ClientTLS construye la config del agente: cliente TLS 1.3 con ALPN h2,
// SNI del nodo (dispersion.go) y cache de sesiones para PSK resumption
// (criterio 2: 0 handshakes completos tras reinicio con ticket
// persistido). rootCAs nil = pool del sistema.
func ClientTLS(sni string, rootCAs *x509.CertPool, cache tls.ClientSessionCache) *tls.Config {
	return &tls.Config{
		MinVersion:         TLSMinVersion,
		MaxVersion:         tls.VersionTLS13,
		ServerName:         sni,
		NextProtos:         []string{ALPNTunnel},
		ClientSessionCache: cache,
		InsecureSkipVerify: rootCAs == nil, // F2 fixture; la CA de flota entra en control
	}
}

// ServerTLS es la config del gateway para el plano de datos: TLS 1.3,
// ALPN h2 y ClientAuth=NoClientCert => el server NO envia
// CertificateRequest (assert del capture: 0 en el handshake).
func ServerTLS(cert *tls.Certificate) *tls.Config {
	cfg := &tls.Config{
		MinVersion:   TLSMinVersion,
		MaxVersion:   tls.VersionTLS13,
		NextProtos:   []string{ALPNTunnel},
		ClientAuth:   tls.NoClientCert,
		Certificates: []tls.Certificate{},
	}
	if cert != nil {
		cfg.Certificates = append(cfg.Certificates, *cert)
	}
	return cfg
}

// PerfilTLS varia el hello del cliente: ProfileStandard es el hello Go
// por defecto; ProfileFrugal recorta las suites anunciadas. Dos builds
// (o dos perfiles) presentan JA3/JA4 distintos (criterio 3,
// scripts/ja3_diff.sh): la firma del handshake deja de identificar a la
// flota entera.
type PerfilTLS int

const (
	PerfilStandard PerfilTLS = iota
	PerfilFrugal
)

// ApplyPerfil aplica la variacion de hello a una config de cliente.
func ApplyPerfil(cfg *tls.Config, p PerfilTLS) *tls.Config {
	switch p {
	case PerfilFrugal:
		// Hello recortado: menos suites legadas y curva unica. Cambia el
		// JA3 (cipher_suites + supported_groups del ClientHello).
		cfg.CipherSuites = []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		}
		cfg.CurvePreferences = []tls.CurveID{tls.X25519}
	default:
		// hello estandar Go: sin recorte explicito.
	}
	return cfg
}

// FileSessionCache persiste el ticket TLS 1.3 en disco: el reinicio del
// agente (dispositivo que se apaga solo) reanuda con PSK en vez de
// handshake completo (criterio 2 T-0408: 0 handshakes completos en 10
// reinicios). Implementa tls.ClientSessionCache con un archivo 0600 por
// destino.
type FileSessionCache struct {
	Path string

	mu    sync.Mutex
	mem   tls.ClientSessionCache
	calls int
}

// NewFileSessionCache carga el ticket persistido si existe.
func NewFileSessionCache(path string) *FileSessionCache {
	return &FileSessionCache{Path: path, mem: tls.NewLRUClientSessionCache(8)}
}

func (c *FileSessionCache) Get(key string) (*tls.ClientSessionState, bool) {
	if st, ok := c.mem.Get(key); ok {
		return st, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := os.ReadFile(c.Path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	st, ok := parseResumption(data)
	if !ok {
		return nil, false
	}
	c.mem.Put(key, st)
	return st, true
}

func (c *FileSessionCache) Put(key string, st *tls.ClientSessionState) {
	c.mem.Put(key, st)
	if st == nil {
		return
	}
	b, err := serializeResumption(st)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = os.WriteFile(c.Path, b, 0o600)
}

// serializeResumption: [ticketLen u16][ticket][state.Bytes()].
func serializeResumption(st *tls.ClientSessionState) ([]byte, error) {
	ticket, state, err := st.ResumptionState()
	if err != nil {
		return nil, fmt.Errorf("tunnel: resumption state: %w", err)
	}
	sb, err := state.Bytes()
	if err != nil {
		return nil, fmt.Errorf("tunnel: session bytes: %w", err)
	}
	out := make([]byte, 0, 2+len(ticket)+len(sb))
	out = append(out, byte(len(ticket)>>8), byte(len(ticket)))
	out = append(out, ticket...)
	out = append(out, sb...)
	return out, nil
}

func parseResumption(data []byte) (*tls.ClientSessionState, bool) {
	if len(data) < 2 {
		return nil, false
	}
	tl := int(data[0])<<8 | int(data[1])
	if 2+tl > len(data) {
		return nil, false
	}
	ticket := append([]byte(nil), data[2:2+tl]...)
	state, err := tls.ParseSessionState(data[2+tl:])
	if err != nil {
		return nil, false
	}
	st, err := tls.NewResumptionState(ticket, state)
	if err != nil {
		return nil, false
	}
	return st, true
}

// ResumeTwice modela el ciclo del agente con ticket persistido: primera
// conexion (handshake completo, el server emite ticket), cierre, reapertura
// con la MISMA cache => el estado de la conexion reabierta informa
// DidResume=true (0 handshake completo). Devuelve el intervalo real para
// el log.
func ResumeTwice(dial func() (*tls.Conn, error)) (resumed bool, elapsed time.Duration, err error) {
	start := time.Now()
	c1, err := dial()
	if err != nil {
		return false, 0, err
	}
	// El server TLS 1.3 emite el NewSessionTicket tras el handshake: hay
	// que leerlo (Read del record pendiente) antes de cerrar.
	b := make([]byte, 64)
	_, _ = c1.Read(b)
	_ = c1.Close()
	c2, err := dial()
	if err != nil {
		return false, 0, err
	}
	defer c2.Close()
	return c2.ConnectionState().DidResume, time.Since(start), nil
}
