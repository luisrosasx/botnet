package proxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SessionTTL es la ventana anti-replay del bot-session (§7, A3-12): un ts
// de mas de 30 s produce 401 interno y el stream no se admite.
const SessionTTL = 30 * time.Second

// skewPermitida absorbe desajuste de reloj cliente/gateway; un ts en el
// futuro dentro de la skew sigue siendo valido.
const skewPermitida = 5 * time.Second

// Errores de auth local. La distincion es observable y exigida por T-0602:
//   - Malformed: "usuario malformado" — rechazado en handshake, sin stream
//     (T-0601 AC3).
//   - Forged: HMAC no corresponde al node_secret — stream cerrado al
//     instante con 0 bytes (AC2).
//   - Expired: ts fuera de la ventana de 30 s -> 401 interno (AC2).
var (
	ErrTokenMalformed = errors.New("proxy: bot-session malformado")
	ErrTokenForged    = errors.New("proxy: bot-session HMAC forjado")
	ErrTokenExpired   = errors.New("proxy: bot-session fuera de TTL")
)

// Status401 es el codigo interno que el agente asocia al expired; el
// SOCKS5 lo registra en log/metrica sin abrir stream.
const Status401 = 401

// SessionIdentity son las cuatro piezas del bot-session mas el ts del
// gateway (A3-12). Es el payload firmado del token.
type SessionIdentity struct {
	ClientID  string
	TenantID  string
	SessionID string
	NodeID    string
	TS        time.Time
}

func (s SessionIdentity) canonical() string {
	return strings.Join([]string{
		s.ClientID, s.TenantID, s.SessionID, s.NodeID,
		strconv.FormatInt(s.TS.Unix(), 10),
	}, "\x1f")
}

func tokenMAC(secret []byte, canonical string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(canonical))
	return m.Sum(nil)
}

// SignToken firma un bot-session con el node_secret (32 B). Mismo patron
// <b64>.<hex> que agent.SignSession; el ts extra es la ventana de 30 s
// que el nodo exige antes de aceptar bytes (el contrato del cable H2 no
// cambia: el token viaja como header de stream).
func SignToken(secret []byte, id SessionIdentity) string {
	can := id.canonical()
	b64 := base64.RawStdEncoding.EncodeToString([]byte(can))
	return b64 + "." + hex.EncodeToString(tokenMAC(secret, can))
}

// ParseToken descompone el token sin verificar el MAC: basta para rechazar
// un "usuario malformado" antes de abrir stream.
func ParseToken(token string) (SessionIdentity, error) {
	var id SessionIdentity
	i := strings.LastIndex(token, ".")
	if i <= 0 || i == len(token)-1 {
		return id, ErrTokenMalformed
	}
	raw, err := base64.RawStdEncoding.DecodeString(token[:i])
	if err != nil {
		return id, ErrTokenMalformed
	}
	want, err := hex.DecodeString(token[i+1:])
	if err != nil || len(want) != sha256.Size {
		return id, ErrTokenMalformed
	}
	parts := strings.Split(string(raw), "\x1f")
	if len(parts) != 5 {
		return id, ErrTokenMalformed
	}
	for _, p := range parts[:4] {
		if p == "" {
			return id, ErrTokenMalformed
		}
	}
	sec, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return id, ErrTokenMalformed
	}
	id.ClientID, id.TenantID, id.SessionID, id.NodeID = parts[0], parts[1], parts[2], parts[3]
	id.TS = time.Unix(sec, 0).UTC()
	return id, nil
}

// VerifyToken valida MAC y ventana en un solo paso local: cero llamadas a
// Redis en el path (A2-14). El MAC se compara en tiempo constante.
func VerifyToken(secret []byte, token string, now time.Time) (SessionIdentity, error) {
	id, err := ParseToken(token)
	if err != nil {
		return id, err
	}
	i := strings.LastIndex(token, ".")
	want, _ := hex.DecodeString(token[i+1:])
	if subtle.ConstantTimeCompare(want, tokenMAC(secret, id.canonical())) != 1 {
		return id, ErrTokenForged
	}
	age := now.UTC().Sub(id.TS)
	if age > SessionTTL || age < -skewPermitida {
		return id, ErrTokenExpired
	}
	return id, nil
}

// LocalAuth es el verificador con contadores: RedisCalls demuestra en log
// el redis_calls=0 del path de auth (T-0602 AC1). Reenvia al secreto en
// memoria del agente; nada sale del nodo.
type LocalAuth struct {
	RedisCalls atomic.Uint64
}

// NewLocalAuth crea el verificador del nodo.
func NewLocalAuth() *LocalAuth { return &LocalAuth{} }

// Verify comprueba un token contra secret. No toca Redis: el contador
// queda en 0 y el log de auth puede afirmar redis_calls=0.
func (a *LocalAuth) Verify(secret []byte, token string, now time.Time) (SessionIdentity, error) {
	return VerifyToken(secret, token, now)
}

// RedisCallsCount es el gauge del path de auth para el assert del test.
func (a *LocalAuth) RedisCallsCount() uint64 { return a.RedisCalls.Load() }

// ErrTokenReplay marca un token ya visto dentro de su ventana.
var ErrTokenReplay = errors.New("proxy: session_replay")

// ReplayGuard recuerda los tokens vistos durante la ventana anti-replay.
// Es memoria del proceso: la ventana es de 30 s y no merece una ida a Redis
// en el camino caliente (A2-14). Un reinicio olvida, y eso solo permite
// repetir un token ya caducado por tiempo.
type ReplayGuard struct {
	mu    sync.Mutex
	visto map[string]time.Time
	now   func() time.Time
}

// NewReplayGuard crea el guardia con el reloj real.
func NewReplayGuard() *ReplayGuard {
	return &ReplayGuard{visto: map[string]time.Time{}, now: time.Now}
}

// Check registra el token y dice si es repeticion. Limpia lo caducado.
func (g *ReplayGuard) Check(token string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	ahora := g.now()
	for t, cuando := range g.visto {
		if ahora.Sub(cuando) > SessionTTL {
			delete(g.visto, t)
		}
	}
	if _, ya := g.visto[token]; ya {
		return ErrTokenReplay
	}
	g.visto[token] = ahora
	return nil
}
