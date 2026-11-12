// Test T-1103 (EP-11): el job purge borra identidad del retirado de
// hace 13 meses y conserva la contabilidad (A3-X5).
package jobs

import (
	"context"
	"testing"

	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestPurgeRGPDFixture(t *testing.T) {
	db := testenv.PG(t)
	if err := RGPDCheck(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	// El mismo fixture dos veces: idempotencia del check (la segunda
	// carrera re-siembra y vuelve a purgar sin quejarse).
	if err := RGPDCheck(context.Background(), db); err != nil {
		t.Fatalf("rgpd-check no idempotente: %v", err)
	}
}
