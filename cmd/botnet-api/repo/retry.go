package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// errTransitorio marca errores de transporte del motor que no son del
// contrato: la conexión se cortó a mitad. En producción (y en el host de
// test con motor portable) un reset de una conexión no debe salir como
// 500 al cliente si un reintento lo resuelve.
func errTransitorio(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, needle := range []string{
		"wsarecv", "forcibly closed", "connection reset", "unexpected EOF",
		"broken pipe", "bad connection", "driver: bad connection",
		"server closed the connection", "connection refused",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// queryRowRetry ejecuta una lectura de una fila reintentando solo errores
// de transporte. `q` recibe el error crudo para poder distinguir
// sql.ErrNoRows. Los reintentos son cortos: si el motor está caído de
// verdad, se devuelve el error en ~1,5 s.
func queryRowRetry(ctx context.Context, q queryRower, query string, args ...any) (string, error) {
	var (
		id  string
		err error
	)
	for i := 0; i < 4; i++ {
		err = q.QueryRowContext(ctx, query, args...).Scan(&id)
		if err == nil || err == sql.ErrNoRows || !errTransitorio(err) {
			return id, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(i+1) * 150 * time.Millisecond):
		}
	}
	return id, err
}

// queryRower es el trozo de *sql.DB que usan las lecturas de una fila.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
