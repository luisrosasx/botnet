# Suelo BOT.NET. Puertas de fase (BLUEPRINT parrafo 15) delegan en scripts/
# cuando existen; mientras, el objetivo hace el minimo honesto.
GO ?= go

.PHONY: build botd-rooted test vet lint race run-stub budget matrix cross qemu-smoke sign verify-release reconcile-hourly metrics-smoke soak-1000 fuzz slo-report metering-test reprobe-guard recruit-cost margin-check rgpd-check reconcile panel-e2e migrate shodan-ingest gw-load contract-test

build:
	$(GO) build ./...

# Windows exige .exe en las rutas ejecutables (Git Bash).
ifeq ($(OS),Windows_NT)
EXE := .exe
else
EXE :=
endif

# EP-14 T-1403 AC2: build del agente con la raiz ed25519 fijada en el
# binario; root_ed25519.pub ya es base64 de los 32 B crudos y se inyecta
# literal (tr quita el \r de CRLF/Git Bash). botd --print-root lo imprime.
ROOT_PUB := deploy/signing/keys/root_ed25519.pub
ROOT_B64 := $(shell tr -d '\r\n' < $(ROOT_PUB) 2>/dev/null)
ROOT_XLFLAG := $(if $(ROOT_B64),-X github.com/botnet/botnet/internal/agent.RootPubKeyBase64=$(ROOT_B64),)

botd-rooted:
	$(GO) build -trimpath -ldflags "$(ROOT_XLFLAG)" -o botd$(EXE) ./cmd/botd

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# lint: golangci-lint si esta instalado; fallback a go vet (sin degradar tests).
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "lint: golangci-lint no instalado -> go vet" && $(GO) vet ./...; fi

# -race exige CGO; solo en CI/servidor (BRIEF-BASE: el enrutador no compila con -race).
race:
	CGO_ENABLED=1 $(GO) test -race ./...

run-stub:
	$(GO) run ./cmd/botd --version

# budget: hoja de recursos del parrafo 2 con margen bruto >=50%; lo endurece EP-10.
# EP-12 T-1206: la hoja agregada se audita sola en F0 (node necesario).
# EP-10 T-1005: la hoja del PLANO DE DATOS (3 partidas nombradas a 1k y
# 100k) y T-1006/T-1008: recluta y margen por clase caen si se rompen.
budget:
	@if test -f scripts/budget.sh; then bash scripts/budget.sh; elif command -v node >/dev/null 2>&1 && test -f scripts/budget/aggregate.mjs; then node scripts/budget/aggregate.mjs --nodes=1000 --concurrency=0.15 --mbps=2 && node --test scripts/budget/aggregate.test.mjs; else echo "budget: requiere node (EP-12 T-1206) o scripts/budget.sh"; fi
	@node scripts/budget/budget.mjs && node --test scripts/budget/budget.test.mjs

# EP-10 T-1001: sesion de 1 GB contra el medidor autoritativo del nodo;
# imprime drift_pct=0.42(<=0.5) — la tolerancia unica alineada a Stripe.
metering-test:
	$(GO) test ./internal/accounting -run 'TestMetering1GB|TestCrashLoss|TestToleranceUnica' -v

# EP-10 T-1007: 0 filas de nodes re-clutadas con optout_at dentro de 12
# meses (nadie volvio a legbar al dueno que se bajo).
reprobe-guard:
	@bash scripts/discovery/reprobe_guard.sh

# EP-11 T-1104: conciliacion diaria usage↔sesiones↔Stripe sobre el
# fixture de 24 h (drift <= 0,5 %) y el fixture de desvio 0,7 %:
# alarma con cobro NO bloqueado (salida 0 con --expect-alarm).
reconcile:
	@node scripts/billing/reconcile.mjs --fixture=scripts/billing/fixtures/reconcile_24h.json
	@node scripts/billing/reconcile.mjs --fixture=scripts/billing/fixtures/reconcile_desvio.json --expect-alarm
	@node --test scripts/billing/reconcile.test.mjs

# EP-11 T-1103: RGPD minimo sobre fixture — el purge deja public_ip
# NULL, conserva mac_hash/device_token_hash y sellado deleted_at con
# la factura intacta. Requiere DATABASE_URL (base con migraciones).
rgpd-check:
	$(GO) run ./cmd/botnet-build rgpd-check

# EP-11 T-1105: humo del panel y la transparencia contra el stack en
# marcha (API en :18080, next start en :3100, DATABASE_URL/REDIS_URL y
# BOTNET_API_URL en el entorno). Runner: node --test (playwright no hay
# en el host). Requiere fuente de env antes: source scripts/env.sh.
panel-e2e:
	@cd web && pnpm -s test

# EP-10 T-1006: USD por exit limpio y churn 15 % a 100k como partida.
recruit-cost:
	@node scripts/budget/recruit_cost.mjs

# EP-10 T-1008: margen bruto >=50 % con el mix debil (70C,20B,10A).
margin-check:
	@node scripts/budget/margin.mjs --mix=70C,20B,10A

matrix:
	@bash scripts/build-matrix.sh

# EP-14 T-1401: matriz cross estatica de 6 archs con CGO_ENABLED=0 y topes
# por clase; es la puerta F1 ampliada (BLUEPRINT §15).
cross:
	@bash scripts/cross/build_matrix.sh

# T-1402: humo QEMU por arch contra scripts/qemu/stub_api.go; sin QEMU en el
# host corre el doble nativo (assert de contrato y endianismo sobre ELF).
qemu-smoke:
	@bash scripts/qemu/smoke.sh

# T-1403: manifiesto ed25519 con raiz fijada en build; sign genera la raiz
# una vez si falta. verify-release falla con exit != 0 ante sig alterado.
sign:
	@bash deploy/signing/sign_release.sh dist/ deploy/signing/keys/root_ed25519.pub

verify-release:
	@bash deploy/signing/verify_release.sh dist/manifest.json deploy/signing/keys/root_ed25519.pub dist/

# T-1407: conciliacion horaria bytes_delta <-> usage sobre fixture de 24 h;
# 0 horas en rojo (tol 0,5 %) con cero requests extra al agente.
reconcile-hourly:
	$(GO) run ./cmd/botnet-build reconcile-hourly

# EP-12 T-1201: /metrics con las 3 metricas del contrato en api, gw y rdv.
metrics-smoke:
	@bash scripts/metrics_smoke.sh

# EP-12 T-1204: soak de reinicios pkill; ITERS=N acelerado, FULL=1 → 1.000.
soak-1000:
	@bash scripts/soak/1000_restarts.sh

# EP-12 T-1205: el guardan barato del corpus seed SIEMPRE corre (CI barata
# < 5 min) y luego el fuzz. Modo largo del job CI (72 h): FUZZTIME=72h. El
# flag -runs del criterio literal (AC1: -runs=1000000) no existe en el
# toolchain fijado (go1.27.4 → «flag provided but not defined: -runs»); su
# equivalente ejecutable es -fuzztime por wall-clock. El corpus descubierto
# se versiona en testdata/corpus/fuzz_*.hex (los crashers caen solos en
# testdata/fuzz/).
fuzz:
	$(GO) test ./internal/tunnel -run TestFuzzFramingSeed
	$(GO) test ./internal/tunnel -run '^$$' -fuzz=FuzzFraming -fuzztime=$(if $(FUZZTIME),$(FUZZTIME),90s)

# EP-12 T-1207: informe SLO sobre fixture (F2 gate) → slo_report.json.
slo-report:
	@bash scripts/soak/slo_report.sh

# EP-01 T-0101: migrations/0002_discovered.up.sql sobre Postgres; el
# delegando exige DATABASE_URL en el entorno.
migrate:
	@bash scripts/discovery/migrate.sh

# EP-01 T-0102: ingesta por lotes Shodan → upsert discovered. Sin FIXTURE
# ni SHODAN_KEY en el entorno, corre el doble determinista de 1.000.
shodan-ingest:
	@FIXTURE="$${FIXTURE:-testdata/shodan_1k.json}" bash scripts/discovery/shodan_ingest.sh

# EP-09 T-0903: carga del gw (1.000 nodos, 4 gateways): p99_ms < 1.500 y
# redirects < 5 %; regenera docs/verification/gw_load_report.json.
gw-load:
	$(GO) test ./cmd/botnet-gw -run GwLoad -v

# EP-08 T-0805: contract-test del contrato cliente (curl contra serve
# real, spec = docs/api/openapi.yaml). "N pass, 0 fail" ⇒ exit 0.
contract-test:
	@bash scripts/api/contract_test.sh
