# The one entry point, for PowerShell. See scripts/cli.ps1 for the verbs.
#
#   ./dev.ps1 test
#   ./dev.ps1 screenshot --device R5CRC2WANMT
#
# The Bash entry point is ./dev and takes the identical verbs.
#
# Stop + catch: an error inside cli.ps1 otherwise arrives here non-terminating
# with $LASTEXITCODE unset, and `exit $LASTEXITCODE` exits 0 (seen with no git).
$ErrorActionPreference = 'Stop'
try {
    & (Join-Path $PSScriptRoot 'scripts/cli.ps1') @args
} catch {
    Write-Host "dev: $_"
    exit 1
}
exit $LASTEXITCODE
