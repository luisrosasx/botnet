# defender-exclusions.ps1 — arregla el falso positivo de Defender con Go.
#
# Windows Defender ponia en cuarentena el binario que el enlazador de Go
# acababa de escribir en %TEMP%\go-build*\exe\a.out.exe, detectado como
# Trojan:Win32/Bearfoos.A!ml (heuristica ML; falso positivo tipico de
# binarios Go). El link fallaba con 0xc0000142 (STATUS_DLL_INIT_FAILED) y
# parecia un fallo de codigo del proyecto.
#
# Este script excluye las rutas donde Go escribe y enlaza, excluye los
# procesos del toolchain y REACTIVA la proteccion en tiempo real (apagarla
# era el sintoma, no el arreglo). Requiere PowerShell elevado.
#
# Uso:  powershell -ExecutionPolicy Bypass -File scripts/dev/defender-exclusions.ps1

$ErrorActionPreference = 'Stop'

$paths = @(
  (Join-Path $env:LOCALAPPDATA 'go-build'),   # cache de compilacion
  (Join-Path $env:USERPROFILE 'go\pkg\mod'),  # modulos descargados
  (Join-Path $env:USERPROFILE 'go-tmp'),      # GOTMPDIR (scripts/env.sh)
  (Join-Path $env:USERPROFILE 'sdk\go'),      # toolchain
  (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path  # el proyecto
)

# Ruido aparte: PUA:Win32/Vigua.A senala un unlocker viejo en D:. No afecta
# al build pero hace trabajar a Defender en cada analisis.
$paths += 'D:\03_PROYECTOS_PERSONALES\HISTORICO\Boxfarm\Software'

$procs = @('go.exe', 'compile.exe', 'link.exe', 'asm.exe', 'cgo.exe')

foreach ($p in $paths) {
  Add-MpPreference -ExclusionPath $p
  Write-Host "exclusion de ruta: $p"
}
foreach ($p in $procs) {
  Add-MpPreference -ExclusionProcess $p
  Write-Host "exclusion de proceso: $p"
}

Set-MpPreference -DisableRealtimeMonitoring $false
Set-MpPreference -SubmitSamplesConsent 1 -SpyNetReporting 2

$st = Get-MpComputerStatus
Write-Host ""
Write-Host ("tiempo real: {0} | antivirus: {1} | firmas: {2}" -f `
  $st.RealTimeProtectionEnabled, $st.AntivirusEnabled, $st.AntivirusSignatureVersion)
Write-Host ("rutas excluidas: {0} | procesos excluidos: {1}" -f `
  (Get-MpPreference).ExclusionPath.Count, (Get-MpPreference).ExclusionProcess.Count)
