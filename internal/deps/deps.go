// Package deps ancla las dependencias compartidas para que `go mod tidy` no las purgue
// antes de que las epics las importen. No expone API.
package deps

import (
	_ "github.com/gofiber/fiber/v2"
	_ "github.com/golang-jwt/jwt/v5"
	_ "github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/nats-io/nats.go"
	_ "github.com/oschwald/geoip2-golang"
	_ "github.com/redis/go-redis/v9"
	_ "github.com/stretchr/testify/require"
	_ "golang.org/x/crypto/ssh"
	_ "golang.org/x/net/http2"
)
