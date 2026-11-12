package main

import (
	"net"
	"time"
)

// Deny-list de destino en el CONNECT (EP-09 T-0907, cierra A3-X2): la
// RFC1918/CGNAT vive EN EL GATEWAY, no en el nodo — el cliente no puede
// martillear la LAN del dueño. Un CONNECT denegado no abre stream ni
// mueve un byte por el tunel; el port-scan del panel se queda en 0 SYN
// en la casa del dueño.

// DenyReason es el motivo del rechazo; viaja al cliente como
// host_unreachable (§7).
const DenyReason = "host_unreachable"

// denyRanges: los cuatro rangos del mandato A3-X2 — 10/8, 172.16/12,
// 192.168/16 y 100.64/10 (CGNAT).
var denyRanges = mustCIDRs("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10")

func mustCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("gw: deny-list CIDR invalido " + c)
		}
		out = append(out, n)
	}
	return out
}

// DeniedIP: true si el destino cae en la LAN del dueño.
func DeniedIP(ip net.IP) bool {
	if len(ip) == 0 {
		return false
	}
	for _, n := range denyRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// DeniedConnect evalua "host:port" (host literal IPv4). Un host de
// nombre no se resuelve aqui (el nodo lo resuelve al establecer); la
// deny-list cubre el caso del panel del router: IPs literales.
func DeniedConnect(hostport string) (bool, string) {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, "" // nombre: no es IP literal, no se puede topar aqui
	}
	if DeniedIP(ip) {
		return true, DenyReason
	}
	return false, ""
}

// ScanGuard: rate-limit por sesion del tenant para la LAN denegada —
// el port-scan no puede ni sondear barato: budget CONNECTs LAN por
// ventana; lo que pasa del budget se salda con 429 (y nunca con SYN).
type ScanGuard struct {
	Window time.Duration
	Budget int
	start  time.Time
	seen   int
	Now    func() time.Time
}

// NewScanGuard: 20 intentos LAN por minuto de session (fixture del
// criterio 3: 1.000 CONNECTs en 1 min -> los 980 siguientes son 429,
// ningun SYN cruza).
func NewScanGuard() *ScanGuard {
	return &ScanGuard{Window: time.Minute, Budget: 20}
}

func (g *ScanGuard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Allow: un CONNECT a LAN denegada consume budget; 429 al agotarse. El
// reloj de ventana es inyectable (reloj acelerado en el test).
func (g *ScanGuard) Allow() (ok bool, code int) {
	t := g.now()
	if g.start.IsZero() || !t.Before(g.start.Add(g.Window)) {
		g.start, g.seen = t, 0
	}
	g.seen++
	if g.seen > g.Budget {
		return false, 429
	}
	return true, 0
}

// EgressCounters: el contador de egress del nodo — solo los CONNECTs
// admitidos mueven bytes por el tunel.
type EgressCounters struct {
	Bytes int64
	Conns int
}

// ConnectDenied: ruta de un CONNECT en el gateway: deny-list -> rechazo
// inmediato (0 bytes de egress); LAN admitida-por-presupuesto no existe
// (la deny-list la cubre); el resto fluye.
func (e *EgressCounters) ConnectDenied(hostport string) (denied bool, reason string) {
	denied, reason = DeniedConnect(hostport)
	if !denied {
		e.Conns++
	}
	return denied, reason
}
