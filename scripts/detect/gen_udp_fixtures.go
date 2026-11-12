// gen_udp_fixtures escribe los fixtures pcap que consume
// scripts/detect/udp_cadence_check.sh (T-1303 AC3). Uso:
//
//	go run ./scripts/detect/gen_udp_fixtures.go testdata/detect
//
// Emite dos capturas Ethernet/IPv4/UDP (DLT_EN10MB, microsegundos LE):
//   - udp_quiet.pcap:     DNS (53) continuo + 2 paquetes propios a 40 min
//     de hueco — el UDP del agente es esporadico: la regla pasa.
//   - udp_c2_beacon.pcap: 24 paquetes propios de 20 B cada 30 s al mismo
//     destino (perfil C2 clasico) — la regla falla.
package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type pcapWriter struct {
	f   *os.File
	buf []byte
	err error
}

func newPcapWriter(path string) *pcapWriter {
	f, err := os.Create(path)
	w := &pcapWriter{f: f}
	if err != nil {
		w.err = err
		return w
	}
	var gh [24]byte
	binary.LittleEndian.PutUint32(gh[0:], 0xa1b2c3d4) // microsegundos, orden del escritor
	binary.LittleEndian.PutUint16(gh[4:], 2)
	binary.LittleEndian.PutUint16(gh[6:], 4)
	binary.LittleEndian.PutUint32(gh[8:], 65535) // snaplen
	binary.LittleEndian.PutUint32(gh[20:], 1)    // DLT_EN10MB
	_, w.err = f.Write(gh[:])
	return w
}

func (w *pcapWriter) close() {
	if w.f != nil {
		_ = w.f.Close()
	}
}

// writePacket graba un datagrama UDP Ethernet/IPv4.
func (w *pcapWriter) writePacket(sec, usec uint32, srcIP, dstIP string, sport, dport uint16, payload []byte) {
	if w.err != nil {
		return
	}
	frame := buildEthUDPIPPacket(srcIP, dstIP, sport, dport, payload)
	var rh [16]byte
	binary.LittleEndian.PutUint32(rh[0:], sec)
	binary.LittleEndian.PutUint32(rh[4:], usec)
	binary.LittleEndian.PutUint32(rh[8:], uint32(len(frame)))
	binary.LittleEndian.PutUint32(rh[12:], uint32(len(frame)))
	w.buf = append(w.buf[:0], rh[:]...)
	w.buf = append(w.buf, frame...)
	_, w.err = w.f.Write(w.buf)
}

func buildEthUDPIPPacket(srcIP, dstIP string, sport, dport uint16, payload []byte) []byte {
	ipLen := 20
	udpLen := 8 + len(payload)
	total := uint16(ipLen + udpLen)
	out := make([]byte, 0, 14+ipLen+udpLen)
	out = append(out, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55) // dst
	out = append(out, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff) // src
	out = append(out, 0x08, 0x00)                         // IPv4
	out = append(out, 0x45, 0x00)                         // vers/IHL, TOS
	out = append(out, byte(total>>8), byte(total))
	out = append(out, 0x00, 0x00, 0x40, 0x00)      // id, flags/frag (no-frag)
	out = append(out, 64, 17, 0x00, 0x00)          // TTL, proto=UDP, csum=0
	out = append(out, net.ParseIP(srcIP).To4()...) // src IP (4 B)
	out = append(out, net.ParseIP(dstIP).To4()...) // dst IP
	udp := []byte{byte(sport >> 8), byte(sport), byte(dport >> 8), byte(dport),
		byte(udpLen >> 8), byte(udpLen), 0x00, 0x00}
	out = append(out, udp...)
	out = append(out, payload...)
	return out
}

func main() {
	dir := "testdata/detect"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	const t0 = 1_700_000_000

	// udp_quiet.pcap: DNS cada 240 s + dos paquetes propios muy separados.
	quiet := newPcapWriter(filepath.Join(dir, "udp_quiet.pcap"))
	for i := 0; i < 12; i++ {
		quiet.writePacket(t0+uint32(i)*240, 0, "192.168.1.10", "1.1.1.1", uint16(40000+i%100), 53, []byte{0x12, 0x34, 0x01, 0x00})
	}
	quiet.writePacket(t0+600, 0, "192.168.1.10", "198.51.100.9", 51234, 443, []byte("probe-one"))
	quiet.writePacket(t0+600+2400, 0, "192.168.1.10", "198.51.100.9", 51234, 443, []byte("probe-two"))
	quiet.close()
	if quiet.err != nil {
		fmt.Fprintln(os.Stderr, quiet.err)
		os.Exit(1)
	}

	// udp_c2_beacon.pcap: beacon propio de 30 s, 24 paquetes, mismo destino.
	beacon := newPcapWriter(filepath.Join(dir, "udp_c2_beacon.pcap"))
	for i := 0; i < 24; i++ {
		beacon.writePacket(t0+uint32(i)*30, 0, "192.168.1.10", "198.51.100.7", 45000, 5544, make([]byte, 20))
	}
	beacon.close()
	if beacon.err != nil {
		fmt.Fprintln(os.Stderr, beacon.err)
		os.Exit(1)
	}
	fmt.Printf("fixtures escritos en %s: udp_quiet.pcap udp_c2_beacon.pcap\n", dir)
}
