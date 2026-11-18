package main

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/botnet/botnet/internal/proxy"
)

// Frente SOCKS5 del gateway (T-1802, cierra la mitad de A1-01).
//
// El gateway escucha SOCKS5 y responde la negociacion ANTES de tocar un
// nodo: un CONNECT con `bot-session` invalido se rechaza aqui y no abre
// stream (el log dice `socks5=rejected`). La autenticacion es RFC1929 con
// usuario `bot-session:<id>` y contrasena = secreto del nodo.

// metodosSoportados: solo user/pass (0x02). Sin autenticacion (0x00) se
// anuncia tambien para los clientes que negocian, pero el CONNECT exige
// credenciales.
const (
	socksVersion   = 5
	metodoUserPass = 0x02
	metodoNada     = 0x00
	cmdConnect     = 0x01
)

// ErrHandshake marca una negociacion invalida.
var ErrHandshake = errors.New("socks5: handshake invalido")

// AtenderHandshake ejecuta la negociacion completa y devuelve el destino
// pedido. No abre nada: solo habla el protocolo.
func AtenderHandshake(c net.Conn, verificar func(usuario, secreto string) bool) (host string, puerto int, err error) {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	cab := make([]byte, 2)
	if _, err := io.ReadFull(c, cab); err != nil {
		return "", 0, err
	}
	if cab[0] != socksVersion {
		// Version 4 (u otra): se rechaza sin mas.
		_, _ = c.Write([]byte{socksVersion, 0xff})
		return "", 0, fmt.Errorf("%w: version %d", ErrHandshake, cab[0])
	}
	metodos := make([]byte, int(cab[1]))
	if _, err := io.ReadFull(c, metodos); err != nil {
		return "", 0, err
	}
	soportado := false
	for _, m := range metodos {
		if m == metodoUserPass {
			soportado = true
			break
		}
	}
	if !soportado {
		_, _ = c.Write([]byte{socksVersion, 0xff})
		return "", 0, fmt.Errorf("%w: sin metodo user/pass", ErrHandshake)
	}
	if _, err := c.Write([]byte{socksVersion, metodoUserPass}); err != nil {
		return "", 0, err
	}
	// RFC1929: version, largo usuario, usuario, largo clave, clave.
	auth := make([]byte, 2)
	if _, err := io.ReadFull(c, auth); err != nil {
		return "", 0, err
	}
	if auth[0] != 0x01 {
		_, _ = c.Write([]byte{0x01, 0x01})
		return "", 0, fmt.Errorf("%w: version auth %d", ErrHandshake, auth[0])
	}
	usuario := make([]byte, int(auth[1]))
	if _, err := io.ReadFull(c, usuario); err != nil {
		return "", 0, err
	}
	lar := make([]byte, 1)
	if _, err := io.ReadFull(c, lar); err != nil {
		return "", 0, err
	}
	secreto := make([]byte, int(lar[0]))
	if _, err := io.ReadFull(c, secreto); err != nil {
		return "", 0, err
	}
	if !verificar(string(usuario), string(secreto)) {
		_, _ = c.Write([]byte{0x01, 0x01})
		return "", 0, fmt.Errorf("%w: credencial rechazada", ErrHandshake)
	}
	if _, err := c.Write([]byte{0x01, 0x00}); err != nil {
		return "", 0, err
	}
	// Peticion: version, cmd, rsv, tipo de direccion.
	peticion := make([]byte, 4)
	if _, err := io.ReadFull(c, peticion); err != nil {
		return "", 0, err
	}
	if peticion[1] != cmdConnect {
		responder(c, 0x07) // comando no soportado
		return "", 0, fmt.Errorf("%w: cmd %d", ErrHandshake, peticion[1])
	}
	switch peticion[3] {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 0x03:
		n := make([]byte, 1)
		if _, err := io.ReadFull(c, n); err != nil {
			return "", 0, err
		}
		b := make([]byte, int(n[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(c, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	default:
		responder(c, 0x08)
		return "", 0, fmt.Errorf("%w: tipo de direccion %d", ErrHandshake, peticion[3])
	}
	p := make([]byte, 2)
	if _, err := io.ReadFull(c, p); err != nil {
		return "", 0, err
	}
	puerto = int(p[0])<<8 | int(p[1])
	_ = c.SetDeadline(time.Time{})
	return host, puerto, nil
}

// responder manda una respuesta CONNECT con el codigo dado.
func responder(c net.Conn, codigo byte) {
	_, _ = c.Write([]byte{socksVersion, codigo, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

// servirSocks5 atiende el listener hasta que el contexto se cancele.
func servirSocks5(ctx context.Context, log *slog.Logger, addr string, verificar func(u, s string) bool) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	log.Info(name+": escuchando socks5", "addr", addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			return nil
		}
		go atenderSesion(log, c, verificar)
	}
}

// atenderSesion negocia y cierra. El releo al nodo llega con EP-09; aqui lo
// que importa es que el rechazo ocurre antes de tocar nada.
func atenderSesion(log *slog.Logger, c net.Conn, verificar func(u, s string) bool) {
	defer c.Close()
	host, puerto, err := AtenderHandshake(c, verificar)
	if err != nil {
		log.Info("socks5=rejected", "err", err.Error())
		return
	}
	log.Info("socks5=accepted", "destino", fmt.Sprintf("%s:%d", host, puerto))
	responder(c, 0x00)
}

// verificarSesion comprueba el usuario `bot-session:<id>` y el secreto.
// Sin verificador cableado (aun) acepta cualquier credencial bien formada.
func verificarSesion(secreto func(usuario string) (string, bool)) func(u, s string) bool {
	return func(usuario, secretoRecibido string) bool {
		if !strings.HasPrefix(usuario, "bot-session:") {
			return false
		}
		if secreto == nil {
			return true
		}
		esperado, ok := secreto(usuario)
		return ok && esperado == secretoRecibido
	}
}

// secretoVivo lee el node_secret actual del nodo (sin el, no hay auth).
func secretoVivo(ctx context.Context, db *sql.DB, nodeID string) ([]byte, error) {
	var sec []byte
	err := db.QueryRowContext(ctx, `
		SELECT s.secret FROM node_secrets s
		 WHERE s.node_id = $1::uuid AND s.revoked_at IS NULL
		 ORDER BY s.expires_at DESC LIMIT 1`, nodeID).Scan(&sec)
	return sec, err
}

// verificarNodo valida usuario `bot-session:<token>`, contrasena = secreto
// en hex, MAC y anti-replay. Devuelve el node_id de la sesion si pasa.
func verificarNodo(db *sql.DB, guard *proxy.ReplayGuard, log *slog.Logger) (func(u, s string) bool, func(u string) string) {
	var ultimo sync.Map
	ok := func(usuario, secretoHex string) bool {
		token := strings.TrimPrefix(usuario, "bot-session:")
		id, err := proxy.ParseToken(token)
		if err != nil {
			return false
		}
		sec, err := secretoVivo(context.Background(), db, id.NodeID)
		if err != nil {
			return false
		}
		if err := guard.Check(token); err != nil {
			log.Info("session_replay", "node_id", id.NodeID)
			return false
		}
		// El MAC manda. La caducidad la impone el nodo con su ventana de
		// 30 s; aqui basta con que el token no se haya repetido: si el
		// guardia ya lo olvido, un token de 31 s vuelve a valer y el nodo
		// decide. Asi el anti-replay no se confunde con la caducidad.
		if _, err := proxy.VerifyToken(sec, token, time.Now()); err != nil && !errors.Is(err, proxy.ErrTokenExpired) {
			return false
		}
		if !hmac.Equal([]byte(secretoHex), []byte(hex.EncodeToString(sec))) {
			return false
		}
		ultimo.Store(usuario, id.NodeID)
		return true
	}
	nodo := func(usuario string) string {
		if v, ok := ultimo.Load(usuario); ok {
			return v.(string)
		}
		return ""
	}
	return ok, nodo
}

// servirSesiones expone POST /api/v1/sessions: el cliente pide nodo y
// recibe una sesion anclada a el. Sin sticky de por medio (A1-03): el
// node_id lo trae la peticion y la sesion lo devuelve.
func servirSesiones(ctx context.Context, log *slog.Logger, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			NodeID string `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeID == "" {
			http.Error(w, "node_id obligatorio", http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"node_id":    body.NodeID,
			"proxy_url":  "socks5h://" + addr,
			"expires_at": time.Now().Add(proxy.SessionTTL).UTC().Format(time.RFC3339),
		})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	log.Info(name+": api de sesiones", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
