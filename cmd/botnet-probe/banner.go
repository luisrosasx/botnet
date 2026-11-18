package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/botnet/botnet/internal/discovery"
	"github.com/botnet/botnet/internal/recruit"
)

// Etapa de banner (T-1702): el banner deja de ser texto suelto y pasa a dar
// clase, modelo y pais. La clasificacion es de internal/discovery, aqui solo
// se persiste. Sin banner no hay candidato.

var recruitClase = recruit.ClaseDeDispositivo

// ejecutarBanner reproduce lo que zgrab2 habria devuelto.
func ejecutarBanner(ctx context.Context, rutaJSON string, dryRun bool) error {
	if rutaJSON == "" {
		return fmt.Errorf("banner sin red: pasa --banner-json con la salida de zgrab2")
	}
	f, err := os.Open(rutaJSON)
	if err != nil {
		return err
	}
	defer f.Close()
	grabs, err := discovery.ParseZgrab2Output(f)
	if err != nil {
		return err
	}
	st, err := abreStore()
	if err != nil {
		return err
	}
	defer st.Close()

	// ParseZgrab2Output descarta los success sin banner; aqui hacen falta
	// igual porque son el caso `no_banner` que se prueba. Se miran aparte.
	sinBannerIP := map[string]bool{}
	ptrDe := map[string]string{}
	if b, err := os.ReadFile(rutaJSON); err == nil {
		var doc map[string]struct {
			Status string `json:"status"`
			Data   struct {
				Ptr    string `json:"ptr"`
				Result struct {
					IP   string `json:"ip"`
					Port int    `json:"port"`
					Data struct {
						Banner string `json:"banner"`
					} `json:"data"`
				} `json:"result"`
			} `json:"data"`
		}
		if json.Unmarshal(b, &doc) == nil {
			for _, e := range doc {
				if e.Data.Ptr != "" {
					ptrDe[e.Data.Result.IP] = e.Data.Ptr
				}
				if e.Status == "success" && strings.TrimSpace(e.Data.Result.Data.Banner) == "" && e.Data.Result.IP != "" {
					sinBannerIP[e.Data.Result.IP] = true
				}
			}
		}
	}

	pais := os.Getenv("RECRUIT_COUNTRY")
	asn := 0
	if v := os.Getenv("RECRUIT_ASN"); v != "" {
		asn, _ = strconv.Atoi(v)
	}
	// Los sin banner no llegan por el parser: se cierran primero.
	for ip := range sinBannerIP {
		id, err := st.CandidatoPorIP(ctx, ip, 80)
		if err != nil {
			continue
		}
		if dryRun {
			fmt.Printf("discard ip=%s reason=no_banner\n", ip)
		} else if err := st.DescartarSinBanner(ctx, id, "probe"); err != nil {
			return err
		}
	}
	var conBanner, excluidos int
	sinBanner := len(sinBannerIP)
	for _, g := range grabs {
		id, err := st.CandidatoPorIP(ctx, g.IP, g.Port)
		if err != nil {
			continue // el candidato no esta en la cola: no inventamos alta
		}
		if strings.TrimSpace(g.Banner) == "" {
			if dryRun {
				fmt.Printf("discard ip=%s port=%d reason=no_banner\n", g.IP, g.Port)
			} else if err := st.DescartarSinBanner(ctx, id, "probe"); err != nil {
				return err
			}
			sinBanner++
			continue
		}
		// Segunda pasada de exclusion: ahora se conoce el PTR.
		if motivo, err := st.Coincide(ctx, g.IP, ptrDe[g.IP]); err == nil && motivo != "" {
			if err := st.Excluir(ctx, id, motivo, "probe"); err != nil {
				return err
			}
			excluidos++
			continue
		}
		tipo, _, modelo := discovery.Classify(g.Banner)
		if dryRun {
			fmt.Printf("banner ip=%s port=%d class=%s model=%q\n", g.IP, g.Port, tipo, modelo)
			continue
		}
		if err := st.MarcarBanner(ctx, id, tipo, modelo, pais, asn); err != nil {
			return err
		}
		conBanner++
	}
	if !dryRun {
		fmt.Printf("banner: con_banner=%d sin_banner=%d\n", conBanner, sinBanner)
	}
	return nil
}

// claseDeDispositivo es el atajo local de recruit.ClaseDeDispositivo para no
// arrastrar el paquete entero en la prueba.
func claseDeDispositivo(tipo string) string { return recruitClase(tipo) }
