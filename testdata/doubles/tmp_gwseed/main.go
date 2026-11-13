package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/internal/backend"
)

func main() {
	ctx := context.Background()
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ro, _ := redis.ParseURL(os.Getenv("REDIS_URL"))
	rdb := redis.NewClient(ro)
	defer rdb.Close()

	tid, _ := backend.NewUUID()
	if _, err := db.ExecContext(ctx, `INSERT INTO tenants (tenant_id, name) VALUES ($1::uuid,'gw-live-seed')`, tid); err != nil {
		panic(err)
	}
	var nodeID string
	if err := db.QueryRowContext(ctx, `SELECT node_id::text FROM nodes WHERE status='live' ORDER BY enrolled_at DESC LIMIT 1`).Scan(&nodeID); err != nil {
		panic(err)
	}
	var sid int64
	err = db.QueryRowContext(ctx, `INSERT INTO sessions (node_id, tenant_id, gw_id) VALUES ($1::uuid,$2::uuid,$3) RETURNING session_id`,
		nodeID, tid, "gw-live-test").Scan(&sid)
	if err != nil {
		panic(err)
	}
	fmt.Println("sesion viva insertada:", sid)
	if err := rdb.Set(ctx, "gw:drain:gw-live-test", "1", time.Minute).Err(); err != nil {
		panic(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var open int
		var reason string
		var ended bool
		_ = db.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE ended_at IS NULL AND gw_id='gw-live-test'`).Scan(&open)
		if open == 0 {
			_ = db.QueryRowContext(ctx, `SELECT closed_reason, ended_at IS NOT NULL FROM sessions WHERE session_id=$1`, sid).Scan(&reason, &ended)
			fmt.Printf("drenada: closed_reason=%s ended=%v\n", reason, ended)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Println("FAIL: sesion sigue viva tras 15 s")
}
