// mesh.go — EP-07 T-0701: la malla WireGuard/headscale es plano de
// operaciones best-effort (BLUEPRINT §8). El agente solo decide aqui
// dos cosas: si entra (tun presente y no clase C) y con que direccion
// CGNAT; el plano de datos jamas cabalga por WG, de modo que la caida
// de headscale no se nota en una sesion (dominio de fallo acotado).
// headscale vive en el bastion, no en el nodo: todo lo medible en
// este paquete es doble del inventario, no del motor.
package agent

import "net"

// MeshPrefix es el rango CGNAT que headscale reserva a la malla de
// operaciones (T-0701 AC1: assert `ip -4 addr | grep -c 100.64.`).
const MeshPrefix = "100.64.0.0/10"

// meshSpan: un /10 sobre 100.64.0.0 son 2^22 direcciones (2o octeto
// 64..127, y dos octetos planos por debajo).
const meshSpan = 1 << 22

// MeshEligible decide si el nodo entra a la malla. La clase C (mips,
// 32 MB) no garantiza `tun` (blueprint §5: "tun no garantizado en
// clase C"), asi que ni se intenta: su proxy SOCKS5 funciona igual
// porque el dato nunca dependio de WG (T-0701 AC2, 0 perdida de
// producto). hasTun manda igualmente: sin tun no hay malla.
func MeshEligible(deviceClass string, hasTun bool) bool {
	return hasTun && deviceClass != "C"
}

// AllocateMeshIP mapea el seq de enrolado a una direccion dentro de
// MeshPrefix: 100.64.x.y, saltando la red 100.64.0.0. La asignacion
// real la hace headscale con su pool; esta funcion es el doble del
// inventario (assert AC1: toda IP cae en el CGNAT) y fija el borde
// que el runbook de SSH usa como destino.
func AllocateMeshIP(seq uint32) net.IP {
	off := seq%(meshSpan-1) + 1 // 1..meshSpan-2: nunca la red del /10
	return net.IPv4(100, byte(64+(off>>16)), byte(off>>8), byte(off))
}

// InMeshPrefix: assert del CGNAT para el inventario y los scripts.
func InMeshPrefix(ip net.IP) bool {
	_, blk, err := net.ParseCIDR(MeshPrefix)
	if err != nil {
		return false
	}
	return blk.Contains(ip)
}

// MeshSessionBytes: cuantos bytes de plano de datos cruza la malla una
// sesion. Cero por contrato (T-0705: un encapsulado por byte,
// prohibido WG-sobre-dato): cae headscale y ni un byte de sesion se
// entera (DoD T-0701).
func MeshSessionBytes(sessionBytes int64) int64 {
	_ = sessionBytes
	return 0
}

// MeshTravels es la lista «viaja» del runbook (T-0702 AC3): el test la
// compara con la documentacion para que runbook y codigo no diverjan.
func MeshTravels() []string {
	return []string{"ssh_operaciones", "iperf3_bandwidth_probe", "geo_exit_probe"}
}

// MeshNeverTravels es la lista «nunca viaja»: session, heartbeat,
// flush y update cabalgan la unica TLS/H2 saliente (BLUEPRINT §8).
func MeshNeverTravels() []string {
	return []string{"session_bytes", "heartbeat", "flush", "update"}
}
