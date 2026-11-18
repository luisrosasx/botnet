package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/hbcodec"
	"github.com/botnet/botnet/pkg/proto"
)

// uuidString formatea 16 B como uuid canonico (mismo layout que
// backend.NewUUID): el frame del agente porta los bytes crudos.
func uuidString(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// decodeHeartbeatBody acepta los dos codecs v1 del agente: el nucleo de
// pkg/proto (>= 200 B, T-0403) y el frame compacto de internal/hbcodec
// (34..106 B, EP-04). Sus longitudes no se solapan, asi que el tamano
// decide sin tentativa ambigua (T-0308 AC2: «unsupported codec
// version» mataba el heartbeat real del agente).
func decodeHeartbeatBody(body []byte, hb *proto.Heartbeat) error {
	if err := hb.Unmarshal(body); err == nil {
		return nil
	}
	var hc hbcodec.Heartbeat
	if err := hc.Decode(body); err == nil {
		*hb = proto.Heartbeat{
			NodeID: hc.NodeID, Seq: hc.Seq, RssKiB: hc.RssKiB,
			UptimeS: uint64(hc.UptimeS), Streams: hc.Streams,
			LossEstimate: hc.LossEstimate, AgentVer: hc.AgentVer,
		}
		return nil
	}
	return hb.Unmarshal(body) // el error del codec canonico es el que se reporta
}

// heartbeat — POST /v1/heartbeat/{node_id} y POST /v1/heartbeat a pelo
// (el agente porta el node_id en la cabecera X-Node-Id o en el propio
// frame; contrato binario de pkg/proto — JSON prohibido en el canal de
// control). Estampa last_seen_at, re-anuda presencia node:{id}:live TTL
// 180 s (§9) y responde 410 con flag bye si el nodo esta retiring|burned:
// la baja es por pull y el agente drena sin esperar al panel (§4,
// injerto P3).
func heartbeat(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		nodeID := c.Params("node_id")
		var hb proto.Heartbeat
		hasFrame := len(c.Body()) > 0
		if hasFrame {
			if err := decodeHeartbeatBody(c.Body(), &hb); err != nil {
				return jsonError(c, http.StatusBadRequest, err.Error())
			}
		}
		if nodeID == "" {
			nodeID = c.Get("X-Node-Id")
		}
		if nodeID == "" && hb.NodeID != ([16]byte{}) {
			nodeID = uuidString(hb.NodeID)
		}
		if nodeID == "" {
			return jsonError(c, http.StatusBadRequest, "falta node_id (ruta, X-Node-Id o frame)")
		}
		status, err := d.Repo.NodeStatus(c.Context(), nodeID)
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "nodo desconocido")
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if status == "retiring" || status == "burned" {
			resp := proto.HeartbeatResponse{Bye: true}
			buf, _ := resp.Marshal()
			c.Set("Content-Type", "application/octet-stream")
			return c.Status(http.StatusGone).Send(buf)
		}
		if err := d.Repo.MarkLive(c.Context(), nodeID, c.IP()); err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		// Historico del panel (T-1202): la fila del latido y el
		// nodes.agent_version salen del mismo frame decodificado. Sin
		// frame no hay telemetria que verter: el ping pelado estampa
		// last_seen_at pero no contamina el p99 con ceros.
		if hasFrame {
			if err := d.Repo.IngestHeartbeat(c.Context(), nodeID, hb.Seq,
				hb.RssKiB, hb.Streams, hb.LossEstimate, hb.AgentVer); err != nil {
				return jsonError(c, http.StatusInternalServerError, err.Error())
			}
		}
		if d.RDB != nil {
			// Presencia caliente: un heartbeat caido no rompe el latido;
			// el TTL 180 s la re-anuda sola tras recuperar Redis (T-0806).
			_ = backend.TouchLive(c.Context(), d.RDB, nodeID, d.Cfg.NodePresenceTTL)
		}
		resp := proto.HeartbeatResponse{NextIntervalS: 120, ConfigRev: 1, PendingCmds: []string{}}
		buf, err := resp.Marshal()
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		c.Set("Content-Type", "application/octet-stream")
		return c.Status(http.StatusOK).Send(buf)
	}
}
