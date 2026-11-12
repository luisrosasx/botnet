// Package loss es la política de conexiones por pérdida medida.
//
// Vive fuera del gateway porque la consumen los dos lados: el gateway
// decide cuántas conexiones tolera el nodo y el agente aplica el
// resultado. Antes de este paquete la función vivía en `package main` del
// gateway y el agente no podía usarla: quedaba código muerto en
// producción (hallazgo H1 del plan de sprints).
package loss

// SecondConnThresholdBPS: 1 % en puntos básicos. Por debajo, una sola
// conexión; la política jamás abre conexiones «por si acaso».
const SecondConnThresholdBPS = 100

// MaxConnsPerNode: techo del blueprint §5 — hasta 4 conexiones H2.
const MaxConnsPerNode = 4

// HysteresisBPS es el margen que hay que perder o ganar para moverse.
// Sin él, un valor oscilando en el umbral abre y cierra conexiones en
// cada latido; con él, el cambio exige una diferencia sostenida.
const HysteresisBPS = 40

// ForLoss es el escalonado puro por pérdida medida: reposo sano
// (≤ 1 %) = 1; > 1 % abre la segunda; > 3 % la tercera; > 6 % la cuarta.
func ForLoss(lossBPS uint16) int {
	if lossBPS <= SecondConnThresholdBPS {
		return 1
	}
	n := 2
	if lossBPS > 300 {
		n = 3
	}
	if lossBPS > 600 {
		n = MaxConnsPerNode
	}
	return n
}

// Apply devuelve el objetivo de conexiones partiendo de `current`.
//
// Sube en cuanto ForLoss pide más (la pérdida duele ya), y baja solo si
// ForLoss pide menos y la pérdida queda por debajo del umbral del escalón
// actual menos HysteresisBPS. Así el descenso exige margen y no oscila.
func Apply(current int, lossBPS uint16) int {
	if current < 1 {
		current = 1
	}
	if current > MaxConnsPerNode {
		current = MaxConnsPerNode
	}
	want := ForLoss(lossBPS)
	if want > current {
		return want
	}
	if want < current {
		// Hay que caer por debajo del suelo del escalón actual menos el
		// margen: así no se baja por rozar el umbral.
		if int(lossBPS) < secondConnFloor(current)-HysteresisBPS {
			return want
		}
	}
	return current
}

// secondConnFloor es la pérdida mínima que justifica `n` conexiones.
func secondConnFloor(n int) int {
	switch {
	case n >= 4:
		return 601
	case n == 3:
		return 301
	case n == 2:
		return 101
	default:
		return 0
	}
}
