// seed_demo: deja un nodo publicable en la base de desarrollo para
// verificar end-to-end la pagina /t/{slug} del panel (EP-11 T-1101).
// Uso: go run ./scripts/panel/seed_demo [slug] [leave_code]
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// replaceDBName cambia el dbname de un DSN URL (formato del repo:
// postgres://user@host:port/db?params). mant. = "postgres" para
// CREATE DATABASE desde el motor.
func replaceDBName(dsn, newDB string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		panic("seed_demo: DATABASE_URL debe ser DSN URL postgres")
	}
	u.Path = "/" + newDB
	return u.String()
}

func newSlug() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))[:12], nil
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL requerido")
		os.Exit(1)
	}
	slug, code := "", ""
	if len(os.Args) > 1 {
		slug = os.Args[1]
	}
	if len(os.Args) > 2 {
		code = os.Args[2]
	}
	if slug == "" {
		var err error
		slug, err = newSlug()
		if err != nil {
			panic(err)
		}
	}
	if code == "" {
		code = "DEMO0001"
	}
	// --createdb: prepara una base dedicada para el panel/dueno
	// (las migraciones completas corren aparte contra ella).
	for i, a := range os.Args {
		if a == "--createdb" && len(os.Args) > i+1 {
			name := os.Args[i+1]
			maintenance := replaceDBName(dsn, "postgres")
			mdb, err := sql.Open("pgx", maintenance)
			if err != nil {
				panic(err)
			}
			defer mdb.Close()
			if _, err := mdb.Exec("CREATE DATABASE " + name); err != nil && !strings.Contains(err.Error(), "already exists") {
				panic(err)
			}
			fmt.Printf("createdb %s ok\n", name)
			return
		}
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()
	var nodeID string
	err = db.QueryRowContext(ctx, `
		INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, model, country, status, bandwidth_cap_kbps, monthly_gb_cap, leave_code, consent_at, last_seen_at)
		VALUES (gen_random_uuid(), $1::char(12), md5($1), 'armv7', 'B', 'TP-Link ER60', 'ES', 'live', 3000, 150, $2::char(8), now(), now())
		ON CONFLICT (slug) DO UPDATE SET leave_code = EXCLUDED.leave_code, status = 'live'
		RETURNING node_id::text`, slug, code).Scan(&nodeID)
	if err != nil {
		panic(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes)
		VALUES (CURRENT_DATE, $1::uuid, 12884901888)
		ON CONFLICT (day, node_id) DO NOTHING`, nodeID); err != nil {
		panic(err)
	}
	fmt.Printf("slug=%s node_id=%s leave_code=%s\n", slug, nodeID, code)
}
