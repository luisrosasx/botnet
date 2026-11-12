package main

import "sort"

// Indice geo→gateways (EP-09 T-0903): cuando el pool local no cubre el
// pais pedido, el gateway responde 307 con Location al gateway cuyo pool
// coincide. Es un RTT de control excepcional: nunca un byte de sesion
// copiado entre gateways (cura del S1 A2-01, §10).

// GeoIndex mapea pais ISO-3166-1 alfa-2 al gateway dueño de ese pool. Con
// varios gateways por pais, el orden estable (id) da el primario.
type GeoIndex struct {
	owners map[string][]string // iso2 -> gw ids ordenados
	addrs  map[string]string   // gw id -> addr publicable
}

func NewGeoIndex() *GeoIndex {
	return &GeoIndex{owners: map[string][]string{}, addrs: map[string]string{}}
}

// Add declara que gwID sirve al pais iso2 desde addr. Re-Add es idempotente.
func (i *GeoIndex) Add(gwID, iso2, addr string) {
	i.addrs[gwID] = addr
	for _, id := range i.owners[iso2] {
		if id == gwID {
			return
		}
	}
	i.owners[iso2] = append(i.owners[iso2], gwID)
	sort.Strings(i.owners[iso2])
}

// Lookup: gateway primario cuyo pool cubre iso2.
func (i *GeoIndex) Lookup(iso2 string) (string, bool) {
	gws := i.owners[iso2]
	if len(gws) == 0 {
		return "", false
	}
	return gws[0], true
}

// Location: valor de la cabecera Location para el 307 (addr del gateway
// dueño); "" si ningun pool cubre el pais (el cliente reintenta con
// otro pais, no se inventa un destino).
func (i *GeoIndex) Location(iso2 string) string {
	gw, ok := i.Lookup(iso2)
	if !ok {
		return ""
	}
	return i.addrs[gw]
}

// Redirect es la respuesta de redireccion de control (AC2).
type Redirect struct {
	Code     int // fijo 307
	Location string
}

// NewRedirect construye el 307 §10; Location vacio = sin pool coincidente
// en todo el indice (caso 404 de enrutado, no 307).
func NewRedirect(iso2 string, idx *GeoIndex) (Redirect, bool) {
	loc := idx.Location(iso2)
	if loc == "" {
		return Redirect{}, false
	}
	return Redirect{Code: 307, Location: loc}, true
}
