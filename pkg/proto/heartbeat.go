package proto

import "fmt"

// HeartbeatResponse es la respuesta del backend al FrameHeartbeat (§4):
// next_interval_s, config_rev, pending_cmds[] y flag bye. Los topes de
// entitlements viajan aqui (T-0306) versionados por config_rev; el valor
// 0/vacio significa "ausente" y el agente conserva el vigente.
type HeartbeatResponse struct {
	NextIntervalS uint16
	ConfigRev     uint32
	PendingCmds   []string
	Bye           bool

	BandwidthCapKbps uint32
	MonthlyGbCap     uint16
	MaxStreams       uint16
	StableVersion    string
}

func (m *HeartbeatResponse) Marshal() ([]byte, error) {
	if len(m.PendingCmds) > 255 {
		return nil, tooLong("pending cmds", len(m.PendingCmds), 255)
	}
	if len(m.StableVersion) > MaxAgentVer {
		return nil, tooLong("stable version", len(m.StableVersion), MaxAgentVer)
	}
	e := &encoder{}
	e.u8(CodecVersion)
	e.u16(m.NextIntervalS)
	e.u32(m.ConfigRev)
	e.strs(m.PendingCmds)
	if m.Bye {
		e.u8(1)
	} else {
		e.u8(0)
	}
	e.u32(m.BandwidthCapKbps)
	e.u16(m.MonthlyGbCap)
	e.u16(m.MaxStreams)
	e.str(m.StableVersion)
	return e.b, nil
}

func (m *HeartbeatResponse) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: HeartbeatResponse: %w", err)
	}
	var err error
	if m.NextIntervalS, err = d.u16(); err == nil {
		m.ConfigRev, err = d.u32()
	}
	if err == nil {
		m.PendingCmds, err = d.strs()
	}
	var bye uint8
	if err == nil {
		bye, err = d.u8()
	}
	if err == nil {
		m.BandwidthCapKbps, err = d.u32()
	}
	if err == nil {
		m.MonthlyGbCap, err = d.u16()
	}
	if err == nil {
		m.MaxStreams, err = d.u16()
	}
	if err == nil {
		m.StableVersion, err = d.str()
	}
	if err != nil {
		return fmt.Errorf("proto: HeartbeatResponse: %w", err)
	}
	m.Bye = bye != 0
	return nil
}

// CmdAckMsg es el mensaje nuevo del plano de control (T-0409): el agente
// acked los pending_cmds recibidos en el heartbeat anterior. Viaja como
// payload de FrameCmdAck (tunel) o en el POST /v1/cmdack (stub F1). Los
// ids son opacos para el backend: el agente deduplica por id y el
// backend los trata como opaque tokens.
type CmdAckMsg struct {
	NodeID [16]byte
	IDs    []string // <= 32 ids, <= 32 B cada uno
}

func (m *CmdAckMsg) Marshal() ([]byte, error) {
	if len(m.IDs) > 32 {
		return nil, tooLong("cmd ack ids", len(m.IDs), 32)
	}
	e := &encoder{}
	e.u8(CodecVersion)
	e.raw(m.NodeID[:])
	e.u8(uint8(len(m.IDs)))
	for _, id := range m.IDs {
		if len(id) > 32 {
			return nil, tooLong("cmd id", len(id), 32)
		}
		e.str(id)
	}
	return e.b, nil
}

func (m *CmdAckMsg) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: CmdAckMsg: %w", err)
	}
	id, err := d.take(16)
	if err == nil {
		copy(m.NodeID[:], id)
	}
	var n uint8
	if err == nil {
		n, err = d.u8()
	}
	m.IDs = nil
	for i := 0; err == nil && i < int(n); i++ {
		var s string
		s, err = d.str()
		if err == nil {
			m.IDs = append(m.IDs, s)
		}
	}
	if err != nil {
		return fmt.Errorf("proto: CmdAckMsg: %w", err)
	}
	return nil
}
