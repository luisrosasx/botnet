package tunnel

import (
	"errors"
	"net"
	"sync"
	"time"
)

// Regla «nueva conexión antes que cola» hasta 4 (BLUEPRINT §5, T-0405,
// A1-19): si un stream debe encolar en la unica conexion y hay perdida
// medida, se abre conexion nueva — segundo carril contra el QoS por flujo
// del ISP. Mas conexiones cuando se mide perdida, nunca por defecto; al
// cesar la perdida, las conexiones sobrantes mueren con GOAWAY.

// ErrMuxClosed: el mux ya no admite streams.
var ErrMuxClosed = errors.New("tunnel: mux closed")

// MuxConfig fija las cotas fisicas: MaxConns=4 (tope duro del plano),
// QueueDepth = streams por conexion a partir de los cuales un stream mas
// "encolaria", ExpandLossBP = umbral de perdida (puntos basicos) que
// habilita el segundo carril, ShrinkQuiet = racha de perdida 0 necesaria
// para contraer (5 min por contrato).
type MuxConfig struct {
	MaxConns     int
	QueueDepth   int
	ExpandLossBP uint16
	ShrinkQuiet  time.Duration
}

// DefaultMuxConfig: 4 conexiones, cola de 25 streams por carril, expandir
// con perdida >= 0,50 % y contraer tras 5 min limpios.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{MaxConns: 4, QueueDepth: 25, ExpandLossBP: 50, ShrinkQuiet: 5 * time.Minute}
}

// muxConn es un carril: una conexion H2 con su contador de streams y su
// sesion (para escribir GOAWAY antes de cerrar).
type muxConn struct {
	sess    *Session
	load    int
	closed  bool
	goaways int
}

// Mux es el controlador de la regla: Placement decide el carril de cada
// stream; NoteLoss alimenta el estimador; Tick/Shrink hacen la
// contraccion. Todos los metodos son seguros para concurrencia.
type Mux struct {
	cfg  MuxConfig
	Dial func() (net.Conn, error) // dial TLS saliente (tls.go)

	mu            sync.Mutex
	conns         []*muxConn
	rr            int
	lossBP        uint16 // ultimo estimado reportado por el agente
	zeroLossSince time.Time
	haveZero      bool
	lost          bool
}

// NewMux crea el mux con su dialer inyectable (tests: net.Pipe/TLS loop).
func NewMux(cfg MuxConfig, dial func() (net.Conn, error)) *Mux {
	if cfg.MaxConns <= 0 {
		cfg = DefaultMuxConfig()
	}
	return &Mux{cfg: cfg, Dial: dial, zeroLossSince: time.Now()}
}

// Placement elige el carril del proximo stream aplicando la regla:
//  1. sin carriles -> abrir el primero;
//  2. todos con la cola llena Y perdida >= umbral Y conns < 4 -> abrir
//     carril nuevo y usarlo (el stream no encola: "nueva antes que cola");
//  3. resto -> round-robin sobre el menos cargado (distribucion pareja,
//     >= 25 % por carril con 100 streams y 4 carriles).
//
// Devuelve el indice del carril elegido.
func (m *Mux) Placement() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lost {
		return 0, ErrMuxClosed
	}
	if len(m.conns) == 0 {
		if err := m.openLocked(); err != nil {
			return 0, err
		}
	}
	minLoad := m.conns[0].load
	minIdx := 0
	for i, c := range m.conns {
		if c.load < minLoad {
			minLoad, minIdx = c.load, i
		}
	}
	if minLoad >= m.cfg.QueueDepth && m.lossBP >= m.cfg.ExpandLossBP && len(m.conns) < m.cfg.MaxConns {
		if err := m.openLocked(); err != nil {
			return 0, err
		}
		minIdx = len(m.conns) - 1
		minLoad = m.conns[minIdx].load
	}
	m.conns[minIdx].load++
	// round-robin de cara al proximo: arranca desde el siguiente carril.
	m.rr = (minIdx + 1) % len(m.conns)
	return minIdx, nil
}

// Release baja la carga cuando el stream termina.
func (m *Mux) Release(idx int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= 0 && idx < len(m.conns) && m.conns[idx].load > 0 {
		m.conns[idx].load--
	}
}

// NoteLoss registra el ultimo estimado de perdida del agente (puntos
// basicos). Con perdida > 0 se resetea la racha limpia; con 0 comienza
// (o continua) la cuenta para la contraccion.
func (m *Mux) NoteLoss(bp uint16, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lossBP = bp
	if bp > 0 {
		m.haveZero = false
		return
	}
	if !m.haveZero {
		m.zeroLossSince, m.haveZero = now, true
	}
}

// CanShrink: la regla de contraccion exige ShrinkQuiet con perdida 0.
func (m *Mux) CanShrink(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.haveZero && !now.Before(m.zeroLossSince.Add(m.cfg.ShrinkQuiet)) && len(m.conns) > 1
}

// Shrink cierra los carriles sobrantes con GOAWAY (criterio 2: 0
// conexiones zombis): sus streams se re-asignan al carril 0 y el conteo
// vuelve a 1. Devuelve cuantos carriles murieron.
func (m *Mux) Shrink(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.haveZero || now.Before(m.zeroLossSince.Add(m.cfg.ShrinkQuiet)) {
		return 0
	}
	dead := 0
	migrated := 0
	for i := len(m.conns) - 1; i > 0; i-- {
		c := m.conns[i]
		migrated += c.load
		m.goawayLocked(c)
		m.conns = m.conns[:i]
		dead++
	}
	if len(m.conns) > 0 {
		m.conns[0].load += migrated
	}
	m.rr = 0
	return dead
}

// Conns: conteo de conexiones vivas (el "ss | grep -c ESTAB" medible).
func (m *Mux) Conns() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conns)
}

// Loads expone la carga por carril para asserts de distribucion.
func (m *Mux) Loads() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, len(m.conns))
	for i, c := range m.conns {
		out[i] = c.load
	}
	return out
}

// Close cierra todos los carriles.
func (m *Mux) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lost = true
	for _, c := range m.conns {
		m.goawayLocked(c)
	}
	m.conns = nil
	return nil
}

func (m *Mux) openLocked() error {
	if m.Dial == nil {
		return errors.New("tunnel: mux sin dialer")
	}
	conn, err := m.Dial()
	if err != nil {
		return err
	}
	m.conns = append(m.conns, &muxConn{sess: NewSession(conn)})
	return nil
}

func (m *Mux) goawayLocked(c *muxConn) {
	if c.closed {
		return
	}
	c.closed = true
	c.goaways++
	if c.sess != nil {
		// Best-effort: el peer puede haber muerto ya; el GOAWAY declara la
		// intencion y el cierre del socket hace el resto.
		_ = c.sess.writeFrame(Header{Kind: KindGoAway}, nil)
		_ = c.sess.Close()
	}
}
