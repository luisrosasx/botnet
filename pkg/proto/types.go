package proto

import (
	"crypto/rand"
	"fmt"
)

const (
	// HeartbeatCoreLen: núcleo fijo del heartbeat, 200 B incluido el byte de
	// versión; el resto hasta 200 B es zona reservada que Marshal rellena con
	// ceros. Crecer los campos consume esa reserva, no el tope de 4 MB.
	HeartbeatCoreLen = 200
	// MaxHeartbeatPad: relleno aleatorio del hueco de versión, 0..55 B,
	// serializado tras el núcleo como [padLen u8][pad]. El total del cable
	// queda en [200, 256] y varía por frame (§5: mata la longitud fija).
	MaxHeartbeatPad = 55
	// MaxAgentVer: longitud máxima de la cadena de versión del agente.
	MaxAgentVer = 32
	// MaxStrLen: techo de toda cadena y blob (prefijo u16).
	MaxStrLen = 65535
)

// EnrollRequest es el body de POST /v1/enroll (BLUEPRINT §4, §6). El
// device_token viaja en cabecera X-Device-Token, nunca aquí ni en la URL.
type EnrollRequest struct {
	Arch     string // mipsbe|mipsle|armv5|armv7|arm64|amd64 (nodes.arch)
	Model    string
	Firmware string
	MAC      string // como lo reporta el dispositivo; el backend guarda el hash
	CSRPEM   []byte // CSR que firma la CA de flota; la clave privada no sale
}

func (m *EnrollRequest) Marshal() ([]byte, error) {
	e := &encoder{}
	e.u8(CodecVersion)
	e.str(m.Arch)
	e.str(m.Model)
	e.str(m.Firmware)
	e.str(m.MAC)
	e.bytes(m.CSRPEM)
	return e.b, nil
}

func (m *EnrollRequest) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: EnrollRequest: %w", err)
	}
	var err error
	if m.Arch, err = d.str(); err == nil {
		m.Model, err = d.str()
	}
	if err == nil {
		m.Firmware, err = d.str()
	}
	if err == nil {
		m.MAC, err = d.str()
	}
	if err == nil {
		m.CSRPEM, err = d.bytes()
	}
	if err != nil {
		return fmt.Errorf("proto: EnrollRequest: %w", err)
	}
	return nil
}

// EnrollResponse es el 201 de POST /v1/enroll (§4): el node_secret (32 B)
// solo viaja aquí y rota con el certificado a 90 días (A3-06).
type EnrollResponse struct {
	NodeID     [16]byte // uuid
	Slug       string   // 12 chars, transparencia GET /t/{slug}
	CertPEM    []byte
	CAPEM      []byte
	NodeSecret [32]byte
	Rendezvous []string // destinos rdv (§5, injerto P1)
	TTLS       uint32
}

func (m *EnrollResponse) Marshal() ([]byte, error) {
	if len(m.Rendezvous) > 255 {
		return nil, tooLong("rendezvous list", len(m.Rendezvous), 255)
	}
	e := &encoder{}
	e.u8(CodecVersion)
	e.raw(m.NodeID[:])
	e.str(m.Slug)
	e.bytes(m.CertPEM)
	e.bytes(m.CAPEM)
	e.raw(m.NodeSecret[:])
	e.strs(m.Rendezvous)
	e.u32(m.TTLS)
	return e.b, nil
}

func (m *EnrollResponse) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: EnrollResponse: %w", err)
	}
	id, err := d.take(16)
	if err == nil {
		copy(m.NodeID[:], id)
		m.Slug, err = d.str()
	}
	if err == nil {
		m.CertPEM, err = d.bytes()
	}
	if err == nil {
		m.CAPEM, err = d.bytes()
	}
	if err == nil {
		var sec []byte
		sec, err = d.take(32)
		if err == nil {
			copy(m.NodeSecret[:], sec)
		}
	}
	if err == nil {
		m.Rendezvous, err = d.strs()
	}
	if err == nil {
		m.TTLS, err = d.u32()
	}
	if err != nil {
		return fmt.Errorf("proto: EnrollResponse: %w", err)
	}
	return nil
}

// Heartbeat es el frame binario fijo del beacon (§4, §5): núcleo de 200 B
// más relleno aleatorio ≤55 B. Porta rss_kib, uptime, streams,
// loss_estimate y ver; la respuesta (no este struct) trae next_interval_s,
// config_rev, pending_cmds[], flag bye y 410 si retiring|burned.
type Heartbeat struct {
	NodeID       [16]byte
	Seq          uint32
	RssKiB       uint32
	UptimeS      uint64
	Streams      uint16
	LossEstimate uint16 // pérdida estimada en puntos básicos (0..10000)
	AgentVer     string // <= MaxAgentVer
	// Pad es el relleno del hueco de versión. nil al construir: Marshal
	// genera uno aleatorio; Unmarshal lo preserva (re-Marshal determinista).
	Pad []byte
}

func (h *Heartbeat) Marshal() ([]byte, error) {
	if len(h.AgentVer) > MaxAgentVer {
		return nil, tooLong("agent ver", len(h.AgentVer), MaxAgentVer)
	}
	pad := h.Pad
	if pad == nil {
		pad = randomPad()
	} else if len(pad) > MaxHeartbeatPad {
		return nil, tooLong("heartbeat pad", len(pad), MaxHeartbeatPad)
	}
	e := &encoder{}
	e.u8(CodecVersion)
	e.raw(h.NodeID[:])
	e.u32(h.Seq)
	e.u32(h.RssKiB)
	e.u64(h.UptimeS)
	e.u16(h.Streams)
	e.u16(h.LossEstimate)
	e.str(h.AgentVer)
	if len(e.b) > HeartbeatCoreLen {
		return nil, tooLong("heartbeat core fields", len(e.b), HeartbeatCoreLen)
	}
	out := make([]byte, HeartbeatCoreLen)
	copy(out, e.b) // zona reservada: ceros
	out = append(out, uint8(len(pad)))
	out = append(out, pad...)
	return out, nil
}

func (h *Heartbeat) Unmarshal(data []byte) error {
	if len(data) < HeartbeatCoreLen || len(data) > HeartbeatCoreLen+1+MaxHeartbeatPad {
		return tooLong("heartbeat frame", len(data), HeartbeatCoreLen+1+MaxHeartbeatPad)
	}
	if data[0] != CodecVersion {
		return fmt.Errorf("proto: Heartbeat: %w", ErrVersion)
	}
	d := &decoder{b: data[:HeartbeatCoreLen], pos: 1}
	id, err := d.take(16)
	if err == nil {
		copy(h.NodeID[:], id)
		h.Seq, err = d.u32()
	}
	if err == nil {
		h.RssKiB, err = d.u32()
	}
	if err == nil {
		h.UptimeS, err = d.u64()
	}
	if err == nil {
		h.Streams, err = d.u16()
	}
	if err == nil {
		h.LossEstimate, err = d.u16()
	}
	if err == nil {
		h.AgentVer, err = d.str()
	}
	if err != nil {
		return fmt.Errorf("proto: Heartbeat: %w", err)
	}
	rest := data[HeartbeatCoreLen:]
	h.Pad = nil
	if len(rest) > 0 {
		padLen := int(rest[0])
		if padLen > MaxHeartbeatPad || len(rest) != 1+padLen {
			return fmt.Errorf("proto: Heartbeat: pad %d, tail %d: %w", padLen, len(rest)-1, ErrInvalidLen)
		}
		h.Pad = append([]byte(nil), rest[1:]...)
	}
	return nil
}

func randomPad() []byte {
	var l [1]byte
	if _, err := rand.Read(l[:]); err != nil {
		l[0] = 7 // mejor relleno determinista que un latido sin relleno
	}
	n := int(l[0]) % (MaxHeartbeatPad + 1)
	if n == 0 {
		return []byte{}
	}
	p := make([]byte, n)
	if _, err := rand.Read(p); err != nil {
		for i := range p {
			p[i] = byte(i)
		}
	}
	return p
}

// HelloCaps es el HELLO del nodo hacia su gateway (§10, injerto P2),
// publicado a Redis node:{id}:caps vía el gateway.
type HelloCaps struct {
	BWMbps      uint16 // medido por iperf3 (bandwidth_cap_kbps sembrado)
	ISO2        string // país del exit, 2 chars
	ASN         uint32
	DeviceClass uint8 // 'A', 'B' o 'C' (nodes.device_class)
}

func (m *HelloCaps) Marshal() ([]byte, error) {
	if len(m.ISO2) > 2 {
		return nil, tooLong("iso2", len(m.ISO2), 2)
	}
	e := &encoder{}
	e.u8(CodecVersion)
	e.u16(m.BWMbps)
	e.str(m.ISO2)
	e.u32(m.ASN)
	e.u8(m.DeviceClass)
	return e.b, nil
}

func (m *HelloCaps) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: HelloCaps: %w", err)
	}
	var err error
	if m.BWMbps, err = d.u16(); err == nil {
		m.ISO2, err = d.str()
	}
	if err == nil {
		m.ASN, err = d.u32()
	}
	if err == nil {
		m.DeviceClass, err = d.u8()
	}
	if err != nil {
		return fmt.Errorf("proto: HelloCaps: %w", err)
	}
	if len(m.ISO2) > 2 {
		return tooLong("iso2", len(m.ISO2), 2)
	}
	return nil
}

// RdvAssign es la respuesta de GET /rdv/assign (§5, injerto P1): rotación
// del destino de la flota sin reentregar el binario.
type RdvAssign struct {
	GatewayAddr  string // host:port del gateway más cercano
	SessionToken []byte // opaco, solo para el handshake con ese gateway
	TTLS         uint32
}

func (m *RdvAssign) Marshal() ([]byte, error) {
	e := &encoder{}
	e.u8(CodecVersion)
	e.str(m.GatewayAddr)
	e.bytes(m.SessionToken)
	e.u32(m.TTLS)
	return e.b, nil
}

func (m *RdvAssign) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: RdvAssign: %w", err)
	}
	var err error
	if m.GatewayAddr, err = d.str(); err == nil {
		m.SessionToken, err = d.bytes()
	}
	if err == nil {
		m.TTLS, err = d.u32()
	}
	if err != nil {
		return fmt.Errorf("proto: RdvAssign: %w", err)
	}
	return nil
}

// SocksGreeting es el greeting SOCKS5 del cliente interno (RFC 1928):
// VER, NMETHODS, METHODS. Embebido por el agente en 127.0.0.1 (§7).
type SocksGreeting struct {
	Version uint8 // 0x05
	Methods []byte
}

func (m *SocksGreeting) Marshal() ([]byte, error) {
	if len(m.Methods) > 255 {
		return nil, tooLong("socks methods", len(m.Methods), 255)
	}
	e := &encoder{}
	e.u8(CodecVersion)
	e.u8(m.Version)
	e.u8(uint8(len(m.Methods)))
	e.raw(m.Methods)
	return e.b, nil
}

func (m *SocksGreeting) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: SocksGreeting: %w", err)
	}
	var err error
	if m.Version, err = d.u8(); err == nil {
		var n uint8
		if n, err = d.u8(); err == nil {
			m.Methods, err = d.take(int(n))
		}
	}
	if err != nil {
		return fmt.Errorf("proto: SocksGreeting: %w", err)
	}
	return nil
}

// SocksRequest es la petición CONNECT del SOCKS5 interno (§7); el gateway
// deniega las ranges LAN antes de abrir stream.
type SocksRequest struct {
	Command uint8  // 0x01 CONNECT
	Address string // host o IP de destino
	Port    uint16
}

func (m *SocksRequest) Marshal() ([]byte, error) {
	e := &encoder{}
	e.u8(CodecVersion)
	e.u8(m.Command)
	e.str(m.Address)
	e.u16(m.Port)
	return e.b, nil
}

func (m *SocksRequest) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: SocksRequest: %w", err)
	}
	var err error
	if m.Command, err = d.u8(); err == nil {
		m.Address, err = d.str()
	}
	if err == nil {
		m.Port, err = d.u16()
	}
	if err != nil {
		return fmt.Errorf("proto: SocksRequest: %w", err)
	}
	return nil
}
