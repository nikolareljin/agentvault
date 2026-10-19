# agentvault overrides for ./dev.ps1. Dot-sourced by scripts/cli.ps1.
# Same verbs and behaviour as scripts/project.sh.

$script:AvApp = 'agentvault.exe'
$script:AvPkg = 'github.com/nikolareljin/agentvault/cmd'
# Pinned: preflight must not run whatever "latest" is on the day.
$script:AvGovulncheckVersion = 'v1.9.0'

function Invoke-Native {
    param([string]$Exe, [string[]]$Arguments)
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Exe $($Arguments -join ' ') exited $LASTEXITCODE" }
}

function Get-AvGoRequired {
    (Select-String -Path (Join-Path $DEV_REPO_ROOT 'go.mod') -Pattern '^go (\S+)').Matches[0].Groups[1].Value
}

# Go 1.21+ downloads the toolchain go.mod names, so any Go from 1.21 on can
# build this. Older or missing Go exits 1 naming the version to install.
function Assert-Go {
    $want = Get-AvGoRequired
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        log_error "Go is not installed. Install Go $want or newer: https://go.dev/dl/"
        exit 1
    }
    $have = ((& go env GOVERSION) -replace '^go', '')
    if ($have -notmatch '^(\d+)\.(\d+)' -or [int]$Matches[1] -lt 1 -or ([int]$Matches[1] -eq 1 -and [int]$Matches[2] -lt 21)) {
        log_error "Go $have is too old to fetch the toolchain. Install Go $want or newer: https://go.dev/dl/"
        exit 1
    }
}

function Get-AvLdflags {
    $version = (Get-Content (Join-Path $DEV_REPO_ROOT 'VERSION') -Raw).Trim()
    # try: Windows PowerShell 5.1 with ErrorActionPreference Stop throws on
    # native stderr, as in a tarball with no .git.
    $commit = $null
    try { $commit = (& git -C $DEV_REPO_ROOT rev-parse --short HEAD 2>$null) } catch { }
    if ($LASTEXITCODE -ne 0 -or -not $commit) { $commit = 'none' }
    # Invariant culture: ':' in a format string is the culture's time separator.
    $date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd'T'HH':'mm':'ss'Z'", [Globalization.CultureInfo]::InvariantCulture)
    "-s -w -X $script:AvPkg.Version=$version -X $script:AvPkg.Commit=$commit -X $script:AvPkg.Date=$date"
}

function Project-Install {
    log_info 'install: submodules'
    Invoke-Native git @('submodule', 'update', '--init', '--recursive')
    Install-DevHooks
    Assert-Go
    log_info 'install: go mod download'
    Invoke-Native go @('mod', 'download')
    log_info 'install: done. Build with ./dev.ps1 build, install the binary with ./dev.ps1 install-bin.'
}

function Project-Build {
    Assert-Go
    if ($DEV_ARGS.Count -gt 0 -and $DEV_ARGS[0] -eq 'bitnet') {
        Not-Applicable 'build bitnet' 'the llama.cpp build needs bash, cmake and a C++ toolchain; use ./dev build bitnet on Linux or macOS'
    }
    Invoke-Native go @('build', '-ldflags', (Get-AvLdflags), '-o', $script:AvApp, '.')
    log_info "build: ./$script:AvApp"
}

function Project-Run {
    Assert-Go
    Invoke-Native go (@('run', '.') + $DEV_ARGS)
}

function Project-Test {
    Assert-Go
    Invoke-Native go @('test', './...')
}

# gofmt and go vet, and vet for the other release targets, as in project.sh.
function Project-Lint {
    Assert-Go
    $unformatted = & gofmt -l .
    if ($unformatted) { throw "lint: not gofmt-formatted: $($unformatted -join ' ')" }
    Invoke-Native go @('vet', './...')
    foreach ($os in @('darwin', 'linux')) {
        $env:GOOS = $os; $env:GOARCH = 'amd64'
        try { Invoke-Native go @('vet', './...') }
        finally { Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue }
    }
}

function Project-Format {
    Assert-Go
    Invoke-Native gofmt @('-w', '.')
}

function Project-Preflight {
    Project-Lint
    Project-Test
    log_info 'preflight: govulncheck'
    Invoke-Native go @('run', "golang.org/x/vuln/cmd/govulncheck@$script:AvGovulncheckVersion", './...')
}

function Project-Clean {
    foreach ($f in @('agentvault.exe', 'agentvault', 'agentvault-bitnet')) {
        Remove-Item -ErrorAction SilentlyContinue -Force (Join-Path $DEV_REPO_ROOT $f)
    }
    log_info 'clean: removed built binaries. Vault and config are untouched.'
}

# Copy the built binary to GOBIN, else GOPATH/bin.
function Project-Install-Bin {
    Project-Build
    $target = (& go env GOBIN)
    if (-not $target) { $target = Join-Path (& go env GOPATH) 'bin' }
    New-Item -ItemType Directory -Force -Path $target | Out-Null
    Copy-Item -Force (Join-Path $DEV_REPO_ROOT $script:AvApp) (Join-Path $target $script:AvApp)
    log_info "installed $script:AvApp to $(Join-Path $target $script:AvApp)"
}

function Project-Doctor {
    $failed = $false
    $want = Get-AvGoRequired
    if (Get-Command go -ErrorAction SilentlyContinue) {
        log_info "doctor: $(& go env GOVERSION) (go.mod asks for $want; newer toolchains are fetched)"
    } else {
        log_error "doctor: Go missing; install Go $want or newer"
        $failed = $true
    }
    if (Get-Command git -ErrorAction SilentlyContinue) {
        log_info "doctor: $(& git --version)"
    } else {
        log_error 'doctor: git missing'
        $failed = $true
    }
    log_info "doctor: script-helpers at $env:SCRIPT_HELPERS_DIR"
    if ($failed) { throw 'doctor: required tools missing' }
}

function Project-Deploy {
    Not-Applicable 'deploy' 'a CLI; releases ship from the release workflow'
}
