// Base scratch EP-06: la crea y la borra (verificacion adversaria).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable"
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		fmt.Println("connect:", err)
		os.Exit(1)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS botnet_scratch_ep06"); err != nil {
		fmt.Println("drop previa:", err)
		os.Exit(1)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE botnet_scratch_ep06"); err != nil {
		fmt.Println("create:", err)
		os.Exit(1)
	}
	var ver string
	_ = admin.QueryRow(ctx, "select version()").Scan(&ver)
	fmt.Println("scratch creada;", ver[:20])
	if _, err := admin.Exec(ctx, "DROP DATABASE botnet_scratch_ep06"); err != nil {
		fmt.Println("drop final:", err)
		os.Exit(1)
	}
	fmt.Println("scratch borrada; EP-06 no toca Postgres (dobles in-process)")
}
