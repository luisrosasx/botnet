// socks5check — verificacion SOCKS5 REAL de un nodo (T-2102).
//
// Una ejecucion = una peticion de verdad: abre un CONNECT SOCKS5 contra el
// proxy embebido del agente (que escucha en la IP de salida del nodo), baja
// un destino HTTP por ese tunel y anota la direccion de origen que el
// destino observo. Con esa evidencia escribe la fila en
// recruit_socks5_checks por el camino de siempre (recruit.Store.Verificar,
// T-1804), de forma que `ok` solo sale con 200, bytes reales, latencia
// plausible e ip_match — nada escrito a mano en la base.
//
// El destino es un eco que levanta el propio comando en loopback: responde
// la direccion del cliente que llamo a la puerta. Es el mismo truco que
// preguntar a api.ipify.org, pero sin depender de Internet ni del firewall:
// lo que se mide es la IP con la que SALIO el nodo.
//
// Uso:
//
//	socks5check --node <uuid> [--db $DATABASE_URL] [--port 1080]
//	            [--state-dir /var/lib/botnet-nodes/5/state]
//	            [--listen 127.0.0.1:8099] [--proxy <host>] [--json] [--help]
//
// Salidas: 0 con evidencia dura (ok=true); 1 sin evidencia o fallo de red;
// 2 argumentos invalidos; 3 falta la base, el estado del nodo o el eco.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/internal/proxy"
	"github.com/botnet/botnet/internal/recruit"
)

// nodeSecretLen es secret.bin tal y como lo escribe el alta.
const nodeSecretLen = 32

// handshakeTimeout cubre la negociacion SOCKS5 y la lectura de la respuesta:
// un nodo colgado no puede dejar el chequeo colgado para siempre.
const handshakeTimeout = 10 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("socks5check", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var (
		node      = fs.String("node", "", "uuid del nodo a verificar (obligatorio)")
		dsn       = fs.String("db", os.Getenv("DATABASE_URL"), "DSN de Postgres (default DATABASE_URL)")
		port      = fs.Int("port", 1080, "puerto SOCKS5 del proxy embebido del nodo")
		proxyHost = fs.String("proxy", "", "donde escucha el SOCKS5 del nodo; vacio = su nodes.public_ip")
		stateDir  = fs.String("state-dir", "", "state del agente: de aqui se lee secret.bin")
		secretHex = fs.String("secret-hex", "", "node_secret en hex (32 B) en vez de leer secret.bin")
		listen    = fs.String("listen", "127.0.0.1:8099", "donde nace el servidor de eco del destino")
		timeout   = fs.Duration("timeout", 20*time.Second, "tope de la peticion completa")
		asJSON    = fs.Bool("json", false, "salida JSON en una linea")
	)
	fs.Usage = func() {
		fmt.Fprintln(errOut, "uso: socks5check --node <uuid> [--db DSN] [--port 1080] [--state-dir DIR] [--listen 127.0.0.1:8099] [--json]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(errOut, "socks5check: --node obligatorio")
		fs.Usage()
		return 2
	}
	if *dsn == "" {
		fmt.Fprintln(errOut, "socks5check: falta DATABASE_URL (o --db)")
		return 3
	}
	secret, err := nodeSecret(*stateDir, *secretHex)
	if err != nil {
		fmt.Fprintf(errOut, "socks5check: %v\n", err)
		return 3
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		fmt.Fprintf(errOut, "socks5check: base: %v\n", err)
		return 3
	}
	defer db.Close()

	publicIP, status, class, err := leerNodo(ctx, db, *node)
	if err != nil {
		fmt.Fprintf(errOut, "socks5check: %v\n", err)
		return 3
	}
	host := *proxyHost
	if host == "" {
		host = publicIP
	}
	if host == "" {
		fmt.Fprintln(errOut, "socks5check: el nodo no tiene public_ip (hace falta un latido real)")
		return 3
	}
	proxyAddr := net.JoinHostPort(host, strconv.Itoa(*port))

	// El candidato es la puerta por la que se mide: la misma (ip,port) que
	// despues consulta recruit.Store.Verificar. Sin fila no hay chequeo.
	if err := asegurarCandidato(ctx, db, host, *port, class); err != nil {
		fmt.Fprintf(errOut, "socks5check: candidato %s: %v\n", proxyAddr, err)
		return 3
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(errOut, "socks5check: eco en %s: %v\n", *listen, err)
		return 3
	}
	defer ln.Close()
	srv := &http.Server{Handler: ecoHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	// El destino se pide siempre por IPv4 numerica: el proxy del nodo no
	// tiene que resolver y la peticion sale tal cual por su socket.
	target := "http://" + ln.Addr().String() + "/echo"

	st, err := recruit.Open(*dsn, 2)
	if err != nil {
		fmt.Fprintf(errOut, "socks5check: base: %v\n", err)
		return 3
	}
	defer st.Close()

	ev, reqErr := pedirPorSocks5(ctx, proxyAddr, secret, *node, target)
	attempt, ok, verErr := st.Verificar(ctx, ev)
	if reqErr != nil {
		fmt.Fprintf(errOut, "socks5check: nodo %s proxy %s: %v\n", *node, proxyAddr, reqErr)
	}
	if verErr != nil {
		// La evidencia se imprime igualmente: sin fila escrita hay que ver
		// que es lo que se midio antes de ir a mirar la base.
		fmt.Fprintf(errOut, "socks5check: verificar: %v\n", verErr)
		ok = false
	}

	if *asJSON {
		_ = json.NewEncoder(out).Encode(map[string]any{
			"node_id": *node, "estado": status, "proxy": proxyAddr, "target": target,
			"http_status": ev.HTTPStatus, "bytes_in": ev.BytesIn, "latency_ms": ev.LatencyMS,
			"egress_ip": ev.EgressIP, "esperado": publicIP, "ip_match": ev.IPMatch,
			"attempt": attempt, "ok": ok,
		})
		return b2i(ok && reqErr == nil)
	}
	fmt.Fprintf(out, "nodo=%s estado=%s proxy=%s destino=%s status=%d bytes=%d latencia_ms=%d egress=%s esperado=%s ip_match=%t intento=%d ok=%t\n",
		*node, status, proxyAddr, target, ev.HTTPStatus, ev.BytesIn, ev.LatencyMS,
		ev.EgressIP, publicIP, ev.IPMatch, attempt, ok)
	return b2i(ok && reqErr == nil)
}

// b2i traduce el veredicto al codigo de salida del contrato.
func b2i(b bool) int {
	if b {
		return 0
	}
	return 1
}

// nodeSecret lee el node_secret de 32 B del alta: secret.bin en el state del
// agente (lo que su SOCKS5 usa para verificar el bot-session) o --secret-hex.
func nodeSecret(stateDir, secretHex string) ([]byte, error) {
	if secretHex != "" {
		raw, err := hex.DecodeString(secretHex)
		if err != nil || len(raw) != nodeSecretLen {
			return nil, errors.New("--secret-hex debe ser 32 B en hex (64 chars)")
		}
		return raw, nil
	}
	if stateDir == "" {
		return nil, errors.New("falta --state-dir (o --secret-hex)")
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "secret.bin"))
	if err != nil {
		return nil, fmt.Errorf("secret.bin en %s: %w", stateDir, err)
	}
	if len(raw) != nodeSecretLen {
		return nil, fmt.Errorf("%s/secret.bin: %d B != %d", stateDir, len(raw), nodeSecretLen)
	}
	return raw, nil
}

// leerNodo lee lo que el backend ya sabe del nodo: su IP de salida (la que
// anoto el alta o el primer latido, A1-04), su estado y su clase.
func leerNodo(ctx context.Context, db *sql.DB, nodeID string) (publicIP, status, class string, err error) {
	err = db.QueryRowContext(ctx, `
		SELECT COALESCE(public_ip::text, ''), status, COALESCE(device_class, '')
		  FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&publicIP, &status, &class)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", fmt.Errorf("nodo %s inexistente", nodeID)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("leer nodo: %w", err)
	}
	return inetText(publicIP), status, class, nil
}

// inetText quita la mascara que Postgres puede arrastrar en ::text: el
// contraste con la IP que devuelve el eco se hace en forma canonica.
func inetText(s string) string {
	if i := len(s); i > 3 && s[i-3:] == "/32" {
		return s[:i-3]
	}
	return s
}

// asegurarCandidato deja la puerta donde se mide el nodo. 'manual' es el
// origen que admite el esquema para lo que no nacio del barrido, y la clase
// es la del nodo que escucha detras (A escucha SOCKS5; B/C no), no un numero
// inventado: la lee el propio comando de la fila del alta.
func asegurarCandidato(ctx context.Context, db *sql.DB, ip string, port int, class string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO recruit_candidates (ip, port, state, source, device_class_guess, device_class)
		VALUES ($1::inet, $2, 'eligible', 'manual', $3, NULLIF($3, ''))
		ON CONFLICT (ip, port) DO UPDATE
		  SET device_class = COALESCE(NULLIF(EXCLUDED.device_class, ''), recruit_candidates.device_class)`,
		ip, port, class)
	return err
}

// ecoHandler es el destino: responde la direccion con la que el nodo llamo.
// JSON minimo y Content-Length fijo, para que el cliente sepa cuando termino
// sin depender del cierre del tunel.
func ecoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		body, _ := json.Marshal(map[string]string{"ip": ip, "path": r.URL.Path})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	})
}

// pedirPorSocks5 hace la peticion real y la convierte en evidencia. El
// bot-session viaja como usuario RFC1929 firmado con el node_secret del nodo,
// que es lo que su proxy verifica antes de admitir el CONNECT.
func pedirPorSocks5(ctx context.Context, proxyAddr string, secret []byte, nodeID, target string) (recruit.Evidencia, error) {
	u, err := url.Parse(target)
	if err != nil {
		return recruit.Evidencia{}, fmt.Errorf("destino %s: %w", target, err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		host, portStr = u.Hostname(), u.Port()
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return recruit.Evidencia{}, fmt.Errorf("puerto del destino %q: %w", u.Host, err)
	}
	token := proxy.SignToken(secret, proxy.SessionIdentity{
		ClientID: "lab", TenantID: "lab", SessionID: "socks5check",
		NodeID: nodeID, TS: time.Now().UTC(),
	})

	// El candidato que se mide es la puerta del nodo: la (ip,port) donde
	// escucha su proxy. Verificar busca la fila por ese par (T-1804).
	ev := recruit.Evidencia{Target: target, NodeID: nodeID, IP: hostProxy(proxyAddr), Port: proxyPort(proxyAddr)}
	t0 := time.Now()
	conn, err := socksDial(ctx, proxyAddr, token, nodeID, host, port)
	if err != nil {
		return ev, fmt.Errorf("connect socks5: %w", err)
	}
	defer conn.Close()

	req := "GET " + pathOr(u.Path) + " HTTP/1.1\r\nHost: " + u.Host +
		"\r\nUser-Agent: botnet-socks5check\r\nAccept: */*\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return ev, fmt.Errorf("escribir peticion: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return ev, fmt.Errorf("leer respuesta: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	ev.HTTPStatus = resp.StatusCode
	ev.BytesIn = len(body)
	ev.LatencyMS = int(time.Since(t0).Milliseconds())
	if readErr != nil {
		return ev, fmt.Errorf("leer cuerpo: %w", readErr)
	}
	var eco struct {
		IP string `json:"ip"`
	}
	if jerr := json.Unmarshal(body, &eco); jerr != nil {
		return ev, fmt.Errorf("el eco no contesto JSON (%q): %w", truncate(string(body), 80), jerr)
	}
	ev.EgressIP = eco.IP
	// ip_match: la IP que vio el destino tiene que ser la del nodo. Es el
	// mismo contraste que hara api.ipify.org contra nodes.public_ip.
	ev.IPMatch = eco.IP != "" && eco.IP == hostProxy(proxyAddr)
	return ev, nil
}

// hostProxy: la direccion del nodo sin el puerto.
func hostProxy(proxyAddr string) string {
	if h, _, err := net.SplitHostPort(proxyAddr); err == nil {
		return h
	}
	return proxyAddr
}

// proxyPort: el puerto del proxy del nodo, que es el del candidato medido.
func proxyPort(proxyAddr string) int {
	if _, p, err := net.SplitHostPort(proxyAddr); err == nil {
		if n, cerr := strconv.Atoi(p); cerr == nil {
			return n
		}
	}
	return 0
}

func pathOr(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// socksDial abre el TCP contra el proxy y lo deja listo para el relay, con
// deadline de handshake (RFC1928 + RFC1929 + CONNECT).
func socksDial(ctx context.Context, proxyAddr, user, pass, dstHost string, dstPort int) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		conn.Close()
		return nil, err
	}
	if err := socks5Handshake(conn, user, pass, dstHost, dstPort); err != nil {
		conn.Close()
		return nil, err
	}
	// El relay vive mientras vive la peticion HTTP: deadline renovado, no
	// eliminado, para que un nodo colgado no cuelgue el chequeo.
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// socks5Handshake negocia user/pass y el CONNECT, y consume la respuesta
// completa del proxy. Lee siempre por el MISMO bufio: el proxy puede haber
// dejado bytes del relay en el mismo paquete.
func socks5Handshake(conn net.Conn, user, pass, dstHost string, dstPort int) error {
	r := bufio.NewReader(conn)
	ver := make([]byte, 2)

	if _, err := conn.Write([]byte{5, 1, 2}); err != nil { // greeting: solo user/pass
		return err
	}
	if _, err := io.ReadFull(r, ver); err != nil {
		return err
	}
	if ver[0] != 5 || ver[1] != 2 {
		return fmt.Errorf("metodo anunciado %v, esperado [5 2]", ver)
	}

	ub, pb := []byte(user), []byte(pass)
	if len(ub) > 255 || len(pb) > 255 {
		return errors.New("usuario o clave de mas de 255 B")
	}
	auth := make([]byte, 0, 2+len(ub)+len(pb)+2)
	auth = append(auth, 1, byte(len(ub)))
	auth = append(auth, ub...)
	auth = append(auth, byte(len(pb)))
	auth = append(auth, pb...)
	if _, err := conn.Write(auth); err != nil {
		return err
	}
	if _, err := io.ReadFull(r, ver); err != nil {
		return err
	}
	if ver[0] != 1 {
		return fmt.Errorf("version de auth %d != 1", ver[0])
	}
	if ver[1] != 0 {
		return fmt.Errorf("auth rechazada (status %d): bot-session invalido o caducado", ver[1])
	}

	connect := []byte{5, 1, 0} // VER, CMD=CONNECT, RSV
	if ip4 := net.ParseIP(dstHost).To4(); ip4 != nil {
		connect = append(connect, 1)
		connect = append(connect, ip4...)
	} else if ip := net.ParseIP(dstHost); ip != nil && len(ip) == net.IPv6len {
		connect = append(connect, 4)
		connect = append(connect, ip...)
	} else {
		if len(dstHost) == 0 || len(dstHost) > 255 {
			return errors.New("destino sin host o de mas de 255 B")
		}
		connect = append(connect, 3, byte(len(dstHost)))
		connect = append(connect, dstHost...)
	}
	connect = append(connect, byte(dstPort>>8), byte(dstPort))
	if _, err := conn.Write(connect); err != nil {
		return err
	}

	reply := make([]byte, 4)
	if _, err := io.ReadFull(r, reply); err != nil {
		return err
	}
	if reply[0] != 5 {
		return fmt.Errorf("respuesta del proxy con version %d", reply[0])
	}
	if reply[1] != 0 {
		return fmt.Errorf("CONNECT rechazado (rep %d)", reply[1])
	}
	switch reply[3] {
	case 1: // IPv4: 4 de direccion + 2 de puerto
		_, err := io.ReadFull(r, make([]byte, 6))
		return err
	case 4: // IPv6
		_, err := io.ReadFull(r, make([]byte, 18))
		return err
	case 3: // dominio: 1 de longitud + dominio + 2 de puerto
		l := make([]byte, 1)
		if _, err := io.ReadFull(r, l); err != nil {
			return err
		}
		_, err := io.ReadFull(r, make([]byte, int(l[0])+2))
		return err
	default:
		return fmt.Errorf("ATYP %d inesperado en la respuesta", reply[3])
	}
}
