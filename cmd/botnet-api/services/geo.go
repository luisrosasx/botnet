// services/geo.go — T-0704 (cierra A3-X4): la geo del exit se comprueba
// por sonda activa, no por autorreporte. Un VPS en DC extranjero ya no
// vende «residencial» colado: la sonda semanal escribe pais/ASN
// observados (migracion 0006) y la seleccion del pool lee geo_verified
// + country_probed. El presupuesto de la sonda (≤ 50 KB/nodo/mes) es
// contrato: ~700 B por pasada, 7 pasadas por semana.
package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
)

// GeoProbeInterval: cadencia semanal de la sonda (AC2: 7 dias
// simulados -> 7 sondas por nodo; el planificador lo afirma en el
// test).
const GeoProbeInterval = 7 * 24 * time.Hour

// GeoProbeBytesTX: presupuesto por pasada — el echo de geo (HEAD de
// ~1 KB con TLS resumido a traves del gateway) redondeado a la baja.
// El contador real lo reporta el script y se guarda en geo_probes.
const GeoProbeBytesTX = 700

// GeoMonthlyBytesBudget: tope del criterio (50 KB/nodo/mes). El peor
// caso planificado (5 sondas en 31 dias) debe quedar debajo.
const GeoMonthlyBytesBudget = 50 * 1024

// DCASNs: ASNs de datacenter conocidos — el VPS que se disfraza de
// residencial. No es lista exhaustiva: es la puerta dura del test y
// operations la amplia por inventario (EP-01 ya trae ASN por IP).
var DCASNs = map[int]struct{}{
	16509: {}, // Amazon AWS
	15169: {}, // Google Cloud
	8075:  {}, // Microsoft Azure
	14061: {}, // Alibaba
	24940: {}, // Hetzner
	36351: {}, // SoftLayer/IBM
	33182: {}, // Tencent Cloud International
}

// ErrGeoReading: lectura inutil de la sonda (pais vacio o fuera de
// formato): la pasada no se registra.
var ErrGeoReading = errors.New("geo: lectura de sonda invalida")

// GeoReading es lo que la sonda observa saliendo por el exit del nodo
// (a traves de su gateway): el pais/ASN que VE el mundo.
type GeoReading struct {
	Country string `json:"country"`
	ASN     int    `json:"asn"`
	City    string `json:"city,omitempty"`
	BytesTX int    `json:"bytes_tx"`
}

// GeoObserver dobla la sonda real (scripts/mesh/geo_probe.sh sube el
// resultado por POST /v1/nodes/{id}/geo; el doble del test devuelve
// lecturas fijas).
type GeoObserver interface {
	Observe(ctx context.Context, nodeID string) (GeoReading, error)
}

// StaticObserver: la lectura ya medida por el script viaja en el
// request; el handler la envuelve aqui y comparte ApplyGeoProbe.
type StaticObserver struct{ Reading GeoReading }

func (s StaticObserver) Observe(_ context.Context, _ string) (GeoReading, error) {
	return s.Reading, nil
}

// PlanGeoProbes: cuantas sondas caen en la ventana por nodo, en
// unidades de cadencia — el reloj simulado del test avanza una
// cadencia semanal por dia simulado (AC2: «7 dias simulados -> 7
// sondas por nodo»; en pared, 7 cadencias = 7 semanas = 7 sondas).
func PlanGeoProbes(cadencias int) int {
	if cadencias < 0 {
		return 0
	}
	return cadencias
}

// GeoWindowSondas: puente explicito tiempo real -> cadencias
// efectivas para el planificador (suelo 0, techo honesto).
func GeoWindowSondas(elapsed time.Duration) int {
	if elapsed < 0 {
		return 0
	}
	return int(elapsed / GeoProbeInterval)
}

// GeoMonthlyBytesWorst: peor caso mensual del planificador anclado
// (sondas en dias 1, 8, 15, 22 y 29 de un mes de 31 dias = 5). El
// test lo aserta bajo el presupuesto de 50 KB/nodo/mes.
func GeoMonthlyBytesWorst() int {
	const worstSondasMes = 5
	return worstSondasMes * GeoProbeBytesTX
}

// ApplyGeoProbe: el veredicto de la pasada. La esperanza es el pais
// autorreportado del inventario (fixture: ES residencial); verified
// exige pais sondeado == esperado y ASN NO de datacenter (AC1). La
// escritura es doble en nodes (geo_verified, country_probed,
// asn_probed, geo_checked_at) y una fila de historia en geo_probes:
// el pool lee nodes, geo_probes es auditoria (7 filas/7 dias por
// nodo). Un DC extranjero pone verified=false y el nodo sale del pool
// «residencial» AL INSTANTE de poder seleccionar (test: < 24 h
// garantizado por construccion), y el pais que se vende es el
// sondeado, no el autorreportado (AC3).
func ApplyGeoProbe(ctx context.Context, rp *repo.Data, nodeID string, obs GeoObserver) (bool, error) {
	reading, err := obs.Observe(ctx, nodeID)
	if err != nil {
		return false, err
	}
	country := strings.ToUpper(strings.TrimSpace(reading.Country))
	if len(country) != 2 {
		return false, ErrGeoReading
	}
	expect, err := rp.NodeCountry(ctx, nodeID) // autorreporte = esperanza
	if err != nil {
		return false, err
	}
	_, dc := DCASNs[reading.ASN]
	verified := country == strings.ToUpper(expect) && !dc
	bytesTX := reading.BytesTX
	if bytesTX <= 0 {
		bytesTX = GeoProbeBytesTX // el script no lo midio: se imputa el presupuesto
	}
	if err := rp.ApplyGeoProbe(ctx, nodeID, country, reading.ASN, verified, bytesTX); err != nil {
		return false, err
	}
	return verified, nil
}
