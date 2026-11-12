// Package config carga la configuración mínima de cada binario desde un
// JSON (--config): defaults válidos para `make run-stub`; el resto de
// claves las añaden las épicas (EP-03 agente, EP-08 API, EP-09 gateway).
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	Endpoint  string `json:"endpoint"`   // URL base del plano de control
	NodeDir   string `json:"node_dir"`   // estado plano del agente /data/botd (BLUEPRINT §3)
	IntervalS int    `json:"interval_s"` // cadencia única de latido+keepalive (§5)
}

func Default() Config {
	return Config{Endpoint: "https://api.botnet.example", NodeDir: "/data/botd", IntervalS: 120}
}

// Load con path vacío devuelve Default; si hay archivo, el JSON se
// superpone campo a campo sobre los defaults (solo presentes en el body).
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}
