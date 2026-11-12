package testenv

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/backend"
)

// Fixtures minimas del backend sobre la base scratch migrada: tenant
// activo, quota del periodo, nodo live con geo y api key con hash
// sha256 de la key en claro que usa el cliente.

// SeedTenant inserta un tenant 'active' y devuelve su uuid.
func SeedTenant(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	id, err := backend.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO tenants (tenant_id, name, plan) VALUES ($1::uuid, $2, 'trial')`, id, name); err != nil {
		t.Fatalf("SeedTenant: %v", err)
	}
	return id
}

// SeedQuota fija la cuota del periodo (YYYY-MM-DD) del tenant.
func SeedQuota(t *testing.T, db *sql.DB, tenantID string, gbAllowance float64, concurrent int) string {
	t.Helper()
	period := time.Now().UTC().Format("2006-01-02")
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO quotas (tenant_id, gb_allowance, concurrent_limit, period_start)
		VALUES ($1::uuid, $2, $3, $4::date)`, tenantID, gbAllowance, concurrent, period); err != nil {
		t.Fatalf("SeedQuota: %v", err)
	}
	return period
}

// SeedNode inserta un nodo live con geo de seleccion y devuelve uuid.
func SeedNode(t *testing.T, db *sql.DB, country, city string, asn int) string {
	t.Helper()
	id, err := backend.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := db.ExecContext(ctx, `
		INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, country, city, asn, status, consent_at, last_seen_at)
		VALUES ($1::uuid, $2, $3, 'amd64', 'B', $4, $5, $6, 'live', now(), now())`,
		id, uniqueSlug(t), uniqueTokenHash(t), country, city, asn)
	if err != nil {
		t.Fatalf("SeedNode: %v", err)
	}
	_ = res
	return id
}

// SeedKey inserta una api key activa cuyo hash es sha256(plain) y
// devuelve el hash.
func SeedKey(t *testing.T, db *sql.DB, tenantID, plain string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(plain))
	hash := hex.EncodeToString(sum[:])
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO api_keys (tenant_id, key_hash, label, mode)
		VALUES ($1::uuid, $2, $3, 'rotating')`, tenantID, hash, "test-"+plain); err != nil {
		t.Fatalf("SeedKey: %v", err)
	}
	return hash
}

var slugSeq int
var tokenSeq int

func uniqueSlug(t *testing.T) string {
	slugSeq++
	s := fmt.Sprintf("t%011d", slugSeq%100000000000)
	if len(s) != 12 {
		t.Fatalf("slug fixture: %q", s)
	}
	return s
}

func uniqueTokenHash(t *testing.T) string {
	tokenSeq++
	return fmt.Sprintf("%064x", tokenSeq)
}
