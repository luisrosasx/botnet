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

// Por qué este fichero existe (D-12): `go mod tidy` purga toda dependencia
// que nadie importe, y las epics se implementaron por fases: si este ancla
// no existiera, tidy borraría las librerías de las capas aún no escritas y
// el primer `go build` de la capa siguiente fallaría por módulo ausente.
// Los imports en blanco son el precio de tener un go.mod estable mientras
// se trocea el trabajo. Se puede retirar cuando todo paquete tenga su uso
// real; hasta entonces, no lo borres por "parecer muerto".
