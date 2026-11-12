package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/pkg/proto"
)

// Gates del alta, en el orden exacto que fija el contrato (csr.json /
// T-0302): token conocido -> 409, sin ticket -> 428, ticket forjado ->
// 403, ticket usado -> 409 (con el node_id del dueno del ticket).
var (
	ErrTokenKnown = errors.New("services: device_token ya enrolado")
	ErrNoTicket   = errors.New("services: falta X-Intrusion-Ticket")
	ErrTicketUsed = errors.New("services: ticket de intrusion ya consumido")
	ErrArch       = errors.New("services: arch fuera de la matriz de builds")
	// ErrNoConsent: alta sin marca de consentimiento del dueno
	// (EP-11 T-1102, A3-10): 428 como el ticket — precondition dura.
	ErrNoConsent = errors.New("services: falta consent_at del dueno")
)

// serialTTL: node:{id}:cert_serial en Redis cubre la vida del cert con
// un dia de margen; es lo que necesita revoke para escribir
// revoked:{serial} sin tocar un almacen de certs.
const serialTTL = CertTTL + 24*time.Hour

// ticketHolderWait: tras el conflicto de indice, el node_id del dueno
// puede no estar commiteado aun; el 409 lo exige ("igual que el camino
// de token conocido"), asi que el lookup se reintenta acotado.
const ticketHolderWait = 100 * time.Millisecond

var fleetArchs = map[string]bool{
	"mipsbe": true, "mipsle": true, "armv5": true,
	"armv7": true, "arm64": true, "amd64": true,
}

// EnrollDeps cablea el service con su infraestructura minima.
type EnrollDeps struct {
	Repo         *repo.Data
	RDB          *redis.Client
	CA           *CA
	TicketSecret []byte
	Rendezvous   []string
}

// ticketHashHex es el sha256 hex del ticket wire: la llave con la que
// el alta estampa nodes.ticket_hash y el indice unico parcial hace de
// unica ganadora (T-0305 AC2/AC3). La unicidad vive en el modelo de
// datos, no en Redis: un FLUSHDB ya no resucita tickets consumidos.
func ticketHashHex(ticketWire string) string {
	sum := sha256.Sum256([]byte(ticketWire))
	return hex.EncodeToString(sum[:])
}

// Enroll aplica los gates y materializa el alta: nodo (con su ticket
// consumido en la misma fila) + secreto vivo + serial de cert en Redis.
// La clave privada nunca aparece aqui: el body solo trae el CSR (A3-02)
// y el secreto sale por esta unica respuesta (A3-06).
func (e *EnrollDeps) Enroll(ctx context.Context, req proto.EnrollAPIRequest, token, ticketWire string) (proto.EnrollAPIResponse, error) {
	tokenHash := sha256.Sum256([]byte(token))
	tokenHex := hex.EncodeToString(tokenHash[:])

	// Gate 1: token conocido -> 409 idempotente (A3-01: el re-enroll no
	// puede emitir certificados que dejen huerfano al agente real). El
	// 409 lleva el node_id existente (T-0302 AC2). El serial del cert
	// vivo SOLO se quema cuando el 409 es un re-enrolado tras reset de
	// fabrica: llega un ticket NUEVO (ningun nodo vivo lo tiene) sobre
	// el nodo existente; ese cert viejo ya no manda y revoke/heartbeat
	// no pueden seguir aceptandolo (T-0308 AC3). Un reintento
	// idempotente del mismo alta (el nodo es el dueno del ticket, o no
	// llega ticket) NO quema el serial: el agente sigue con su cert.
	if id, err := e.Repo.NodeByTokenHash(ctx, tokenHex); err != nil {
		return proto.EnrollAPIResponse{}, err
	} else if id != "" {
		freshTicket := false
		if ticketWire != "" {
			holder, herr := e.Repo.NodeByTicketHash(ctx, ticketHashHex(ticketWire))
			if herr != nil {
				return proto.EnrollAPIResponse{NodeID: id}, herr
			}
			freshTicket = holder == "" // ticket virgen = re-enrolado tras reset
		}
		if freshTicket {
			if serial, gerr := e.RDB.Get(ctx, "node:"+id+":cert_serial").Result(); gerr == nil && serial != "" {
				if err := backend.MarkRevoked(ctx, e.RDB, serial, serialTTL); err != nil {
					return proto.EnrollAPIResponse{NodeID: id}, err
				}
				_ = e.RDB.Del(ctx, "node:"+id+":cert_serial").Err() // el viejo ya esta en la CRL
			}
		}
		return proto.EnrollAPIResponse{NodeID: id}, ErrTokenKnown
	}

	// Gate 2: sin ticket -> 428 (la puerta de intrusion es obligatoria;
	// el device_token viaja en cabecera, la URL no referencia nada).
	if ticketWire == "" {
		return proto.EnrollAPIResponse{}, ErrNoTicket
	}
	// Gate 3: HMAC del ticket -> 403 si forjado. El token embebido NO
	// se equipara contra el presentador: un ticket valido y libre lo
	// gana quien lo traiga primero, y el perdedor sale 409 por el
	// indice unico — no 403 (T-0305 AC3: reparto 1x201 + 99x409).
	if _, err := proto.VerifyTicket(e.TicketSecret, ticketWire); err != nil {
		return proto.EnrollAPIResponse{}, proto.ErrTicketForged
	}
	// Gate 3.5: consentimiento del dueno (EP-11 T-1102): sin marca
	// no hay alta — 428 y el ticket NO se consume, para que el retry
	// con consentimiento llegue limpio.
	consentAt := time.Now().UTC()
	if req.ConsentAt != "" {
		t, cerr := time.Parse(time.RFC3339, req.ConsentAt)
		if cerr != nil {
			return proto.EnrollAPIResponse{}, fmt.Errorf("services: consent_at ilegible: %w", cerr)
		}
		consentAt = t.UTC()
	} else {
		return proto.EnrollAPIResponse{}, ErrNoConsent
	}

	if !fleetArchs[req.Arch] {
		return proto.EnrollAPIResponse{}, ErrArch
	}

	certPEM, serial, notAfter, err := e.CA.SignCSR([]byte(req.CSRPEM), time.Now().UTC())
	if err != nil {
		return proto.EnrollAPIResponse{}, err
	}

	nodeID, err := backend.NewUUID()
	if err != nil {
		return proto.EnrollAPIResponse{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return proto.EnrollAPIResponse{}, fmt.Errorf("services: node_secret: %w", err)
	}
	macHash := sha256.Sum256([]byte(req.MAC))
	ticketHash := ticketHashHex(ticketWire)

	// Gate 4: el alta inserta el ticket consumido en la MISMA fila del
	// nodo y el indice unico parcial nodes_ticket_hash_live_uniq decide
	// la ganadora (T-0305 AC2/AC3): el conflicto de indice es 409 con
	// el node_id del dueno, igual que el camino de token conocido.
	// slug char(12) UNIQUE: reintento acotado; con 62^12 es cosmico.
	var slug string
	for attempt := 0; attempt < 3; attempt++ {
		slug, err = proto.NewSlug()
		if err != nil {
			return proto.EnrollAPIResponse{}, err
		}
		err = e.Repo.InsertNode(ctx, nodeID, slug, tokenHex, ticketHash, req.Arch, req.Model, req.Firmware, macHash[:], consentAt)
		if err == nil {
			break
		}
		if errors.Is(err, repo.ErrTicketTaken) {
			return proto.EnrollAPIResponse{NodeID: e.ticketHolder(ctx, ticketHash)}, ErrTicketUsed
		}
		if attempt == 2 {
			return proto.EnrollAPIResponse{}, fmt.Errorf("services: insert node: %w", err)
		}
	}
	if err := e.Repo.InsertSecret(ctx, nodeID, secret, notAfter); err != nil {
		return proto.EnrollAPIResponse{}, err
	}
	if err := e.RDB.Set(ctx, "node:"+nodeID+":cert_serial", serial, serialTTL).Err(); err != nil {
		return proto.EnrollAPIResponse{}, fmt.Errorf("services: serial store: %w", err)
	}

	return proto.EnrollAPIResponse{
		NodeID:           nodeID,
		Slug:             slug,
		CertPEM:          string(certPEM),
		CAPEM:            string(e.CA.CertPEM),
		NodeSecretB:      base64.StdEncoding.EncodeToString(secret),
		Rendezvous:       e.Rendezvous,
		TTLS:             uint32(CertTTL / time.Second),
		BandwidthCapKbps: 2048,
		MonthlyGbCap:     150,
		MaxStreams:       150,
		ConfigRev:        1,
	}, nil
}

// ticketHolder resuelve el dueno vivo del ticket ya consumido. Tras el
// conflicto de indice la ganadora puede ir por delante sin commitear:
// reintentos cortos antes de devolver el 409 sin node_id (peor caso;
// cualquier replay posterior ya lo encuentra).
func (e *EnrollDeps) ticketHolder(ctx context.Context, ticketHash string) string {
	for i := 0; i < 5; i++ {
		id, err := e.Repo.NodeByTicketHash(ctx, ticketHash)
		if err != nil || id != "" {
			return id
		}
		time.Sleep(ticketHolderWait)
	}
	return ""
}
