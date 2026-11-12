package agent

import (
	"net"

	"github.com/botnet/botnet/pkg/proto"
)

// HELLO de capacidades (EP-09 T-0901, §10 injerto P2): el nodo publica
// {bw_mbps, iso2, asn, device_class} hacia su gateway. El HELLO viaja por
// el tunel existente — una sola conexion por tramo (AC2: 0 conexiones TCP
// nuevas) — nunca abre un canal propio.

// CapsSource es la fuente local de capacidades del nodo: ancho medido por
// la sonda iperf3 de la malla (T-07xx), pais/ASN del exit verificado
// (geo_verified, A3-X4) y la clase del inventario (nodes.device_class).
type CapsSource interface {
	BWMbps() uint16
	ISO2() string
	ASN() uint32
	DeviceClass() uint8
}

// BuildHelloCaps materializa el HelloCaps desde la fuente local. Un iso2
// invalido se degrada a vacio: el gateway publica igual el resto de
// capacidades y el rdv re-asignara por pais en cuanto la sonda sanee.
func BuildHelloCaps(s CapsSource) proto.HelloCaps {
	var c proto.HelloCaps
	if s == nil {
		return c
	}
	c.BWMbps = s.BWMbps()
	c.ASN = s.ASN()
	c.DeviceClass = s.DeviceClass()
	if iso := s.ISO2(); len(iso) == 2 {
		c.ISO2 = iso
	}
	return c
}

// HelloFrame codifica el HELLO como payload de FrameHello dentro de un
// CtrlFrame del canal de control existente.
func HelloFrame(caps proto.HelloCaps, nodeID [16]byte, seq uint32) ([]byte, error) {
	payload, err := caps.Marshal()
	if err != nil {
		return nil, err
	}
	return proto.EncodeFrame(&proto.CtrlFrame{
		Type:    proto.FrameHello,
		NodeID:  nodeID,
		Seq:     seq,
		Payload: payload,
	})
}

// ConnWriter es el tramo del tunel ya conectado: HelloSender escribe en
// la conexion existente y jamas marca (de ahi el contador de dobles).
type ConnWriter interface {
	Write(p []byte) (int, error)
}

// HelloSender envia el HELLO reutilizando el tunel. dial es nulo por
// diseno: quien llama pasa el ConnWriter vivo; si el tunel murio, el
// supervisor reconecta y aqui solo se reemite caps.
type HelloSender struct {
	Conn   ConnWriter
	NodeID [16]byte
	seq    uint32
}

// Send serializa caps y lo escribe en la conexion existente. Devuelve el
// numero de seq usado para el idempotente re-intento.
func (h *HelloSender) Send(caps proto.HelloCaps) (uint32, error) {
	wire, err := HelloFrame(caps, h.NodeID, h.seq+1)
	if err != nil {
		return h.seq, err
	}
	n, err := h.Conn.Write(wire)
	if err != nil {
		return h.seq, err
	}
	if n != len(wire) {
		return h.seq, proto.ErrInvalidLen
	}
	h.seq++
	return h.seq, nil
}

// CountingDialer dobla el establecimiento TCP del tunel para los asserts
// "0 conexiones TCP nuevas para las capacidades" (AC2): el HELLO se emite
// sobre el tunel ya vivo y nunca pasa por Dial.
type CountingDialer struct {
	Dials int
	Conn  net.Conn // tunel del tramo, abastecido por el llamante/tests
	Err   error
}

// Dial: unica puerta de entrada TCP del tramo.
func (d *CountingDialer) Dial() (net.Conn, error) {
	d.Dials++
	return d.Conn, d.Err
}
