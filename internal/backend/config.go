// Package backend: lógica compartida del plano de control (BLUEPRINT §9,
// handler → service → repo con SQL plano). Aqui viven la config por
// entorno, los ids y las piezas que el gateway consume igual que la API
// («botnet-gw consume los mismos services: sin lógica de negocio en el
// gateway»): cache de api keys, CRL y modo degradado.
package backend

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config del backend tomada del entorno. Sin secretos compilados: en
// produccion todo llega por variables; los defaults sirven solo al
// `serve` local y a los tests del repo.
type Config struct {
	DatabaseURL     string
	RedisURL        string // default redis://127.0.0.1:6379
	NATSURL         string // vacio = plano NATS apagado
	GWAddr          string // host:port del socks5h del gateway
	AdminToken      string // bearer para /v1/nodes/{id}/revoke
	TicketSecret    []byte // HMAC compartido con la sonda (X-Intrusion-Ticket)
	Rendezvous      []string
	CACertPEM       string // ruta PEM de la CA de flota (offline traida al signer)
	CAKeyPEM        string
	SessionTTLDur   time.Duration
	RateLimitRPS    int64
	APIKeyCacheTTL  time.Duration
	CRLRefreshDur   time.Duration
	NodePresenceTTL time.Duration
}

// DevTicketSecret: secreto de 32 B estable para desarrollo/tests, para
// que el contrato de ticket sea reproducible sin infraestructura.
const devTicketSecretHex = "5bd1ce7f4a2e9b0c8d7f6a5b4c3d2e1f00112233445566778899aabbccddeeff"

// ConfigFromEnv lee el entorno con defaults coherentes con el runbook.
func ConfigFromEnv() Config {
	cfg := Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		RedisURL:        envOr("REDIS_URL", "redis://127.0.0.1:6379"),
		NATSURL:         os.Getenv("NATS_URL"),
		GWAddr:          envOr("GW_ADDR", "gw.botnet.net:1080"),
		AdminToken:      envOr("API_ADMIN_TOKEN", "dev-admin-token"),
		Rendezvous:      splitCSV(envOr("RDV_URLS", "https://rdv.botnet.net/rdv/assign")),
		CACertPEM:       os.Getenv("CA_CERT_PEM"),
		CAKeyPEM:        os.Getenv("CA_KEY_PEM"),
		SessionTTLDur:   30 * time.Minute,
		RateLimitRPS:    int64Env("RATE_LIMIT_RPS", 60),
		APIKeyCacheTTL:  300 * time.Second,
		CRLRefreshDur:   55 * time.Second,
		NodePresenceTTL: 180 * time.Second,
	}
	if sec := os.Getenv("TICKET_SECRET"); sec != "" {
		raw, err := hex.DecodeString(sec)
		if err != nil || len(raw) != 32 {
			panic("backend: TICKET_SECRET debe ser 32 B en hex (64 chars)")
		}
		cfg.TicketSecret = raw
	} else {
		cfg.TicketSecret, _ = hex.DecodeString(devTicketSecretHex)
	}
	return cfg
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func int64Env(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// NewUUID: uuid v4 con crypto/rand, formateado canonico. Se evita la
// libreria google/uuid para no subir una dependencia directa nueva.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("backend: uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
