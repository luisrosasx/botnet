package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, "postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "conexion base madre:", err)
		os.Exit(1)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS botnet_ep05_adv WITH (FORCE)`); err != nil {
		fmt.Fprintln(os.Stderr, "drop previo:", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE botnet_ep05_adv`); err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	var n string
	if err := admin.QueryRow(ctx, `SELECT current_database()`).Scan(&n); err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(1)
	}
	fmt.Println("scratch creada y consultada:", n)
	if _, err := admin.Exec(ctx, `DROP DATABASE botnet_ep05_adv`); err != nil {
		fmt.Fprintln(os.Stderr, "drop final:", err)
		os.Exit(1)
	}
	fmt.Println("scratch borrada OK")
}
