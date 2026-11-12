package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
)

// DrainBudget: el stream vivo de un nodo revocado se drena con GOAWAY en
// <30 s (§ T-0803 AC2); es el techo del bucle de drenaje del gateway, no
// una cuenta de resultados — el backend cierra las filas al instante.
const DrainBudget = 30 * time.Second

// RevokeResult es lo que devuelve POST /v1/nodes/{id}/revoke.
type RevokeResult struct {
	NodeID        string `json:"node_id"`
	Status        string `json:"status"`
	CertSerial    string `json:"cert_serial,omitempty"`
	CertRevoked   bool   `json:"cert_revoked"`
	SecretsRevoke int    `json:"secrets_revoked"`
	SessionsClosd int    `json:"sessions_closed"`
}

// RevokeNode quema a un nodo (quemado o traidor): en Redis queda
// revoked:{serial} para el CRL del gateway (puerta cerrada <60 s sin
// reiniciarle), en Postgres mueren el node_secret vivo y las sesiones
// abiertas con closed_reason tipada, y nodes.status pasa a 'burned' —
// el heartbeat le respondera 410 y el agente drenara solo (§4).
func RevokeNode(ctx context.Context, rdb *redis.Client, rp *repo.Data, nodeID string) (RevokeResult, error) {
	status, err := rp.NodeStatus(ctx, nodeID)
	if errors.Is(err, repo.ErrNotFound) {
		return RevokeResult{}, err
	}
	if err != nil {
		return RevokeResult{}, err
	}
	out := RevokeResult{NodeID: nodeID, Status: "burned"}

	serial, err := rdb.Get(ctx, "node:"+nodeID+":cert_serial").Result()
	if err != nil && err != redis.Nil {
		return RevokeResult{}, fmt.Errorf("services: leer serial: %w", err)
	}
	if serial != "" {
		out.CertSerial = serial
		if err := backend.MarkRevoked(ctx, rdb, serial, serialTTL); err != nil {
			return RevokeResult{}, err
		}
		out.CertRevoked = true
	}
	if n, err := rp.RevokeSecrets(ctx, nodeID); err != nil {
		return RevokeResult{}, err
	} else {
		out.SecretsRevoke = n
	}
	if n, err := rp.CloseOpenForNode(ctx, nodeID, "goaway"); err != nil {
		return RevokeResult{}, err
	} else {
		out.SessionsClosd = n
	}
	if err := rp.SetNodeStatus(ctx, nodeID, "burned"); err != nil {
		return RevokeResult{}, err
	}
	_ = status // el estado previo solo importa para el 404
	return out, nil
}
