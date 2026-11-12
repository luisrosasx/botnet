// Frescura del inventario (T-1202 AC3) medida contra la fila real: el
// heartbeat del agente entra por POST /v1/heartbeat, la ingestion deja una
// fila en node_heartbeats y pone nodes.agent_version al dia; el test LEE
// ambas fuentes Postgres y compara — ya no vale leer del frame el mismo
// string que se le paso a Collect (identidad por construccion). Los
// valores de telemetria son deliberadamente distintos de cualquier
// constante del test para que la fila, y no la identidad, decida.
package handlers

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/internal/backend/testenv"
	"github.com/botnet/botnet/internal/hbcodec"
)

func TestHeartbeatFreshnessFromRow(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 902)

	idBytes, err := agent.ParseUUID(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	frame := hbcodec.Heartbeat{
		NodeID:       idBytes,
		Seq:          42,
		RssKiB:       4242,
		UptimeS:      100,
		Streams:      9,
		LossEstimate: 77,
		AgentVer:     "v0.4.2-rc7",
	}
	buf, err := frame.Encode()
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(&Deps{Repo: repo.New(db)})
	req := httptest.NewRequest("POST", "/v1/heartbeat/"+nodeID, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("heartbeat: quiero 200, tengo %d", resp.StatusCode)
	}

	ctx := context.Background()
	// Fila real del historico: lo que pinta el panel (rss/loss/ver).
	var rowRSS, rowStreams, rowLoss int
	var rowVer string
	if err := db.QueryRowContext(ctx, `
		SELECT rss_kib, streams, loss_estimate, agent_version
		  FROM node_heartbeats
		 WHERE node_id = $1::uuid
		 ORDER BY ts DESC LIMIT 1`, nodeID).
		Scan(&rowRSS, &rowStreams, &rowLoss, &rowVer); err != nil {
		t.Fatalf("fila node_heartbeats: %v", err)
	}
	if rowRSS != int(frame.RssKiB) || rowStreams != int(frame.Streams) ||
		rowLoss != int(frame.LossEstimate) || rowVer != frame.AgentVer {
		t.Fatalf("fila historica = (%d,%d,%d,%q), want (%d,%d,%d,%q)",
			rowRSS, rowStreams, rowLoss, rowVer,
			frame.RssKiB, frame.Streams, frame.LossEstimate, frame.AgentVer)
	}

	// Inventario al dia: nodes.agent_version coincide con la ver del
	// ultimo latido en < 1 heartbeat (< 1 s de reloj real).
	var nodeVer string
	var lastBeatFresh bool
	if err := db.QueryRowContext(ctx, `
		SELECT n.agent_version,
		       (SELECT h.agent_version FROM node_heartbeats h
		         WHERE h.node_id = n.node_id ORDER BY h.ts DESC LIMIT 1) = n.agent_version
		  FROM nodes n WHERE node_id = $1::uuid`, nodeID).
		Scan(&nodeVer, &lastBeatFresh); err != nil {
		t.Fatalf("nodes.agent_version: %v", err)
	}
	if !lastBeatFresh || nodeVer != frame.AgentVer {
		t.Fatalf("inventario rancio: nodes=%q vs ultimo latido=%q (fresco=%v)",
			nodeVer, frame.AgentVer, lastBeatFresh)
	}
}
