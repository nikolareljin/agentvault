# agentvault overrides for ./dev.ps1. Dot-sourced by scripts/cli.ps1.
# Same verbs as scripts/project.sh.

function Invoke-Native {
    param([string]$Exe, [string[]]$Arguments)
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Exe $($Arguments -join ' ') exited $LASTEXITCODE" }
}

function Assert-Go {
    $want = (Select-String -Path (Join-Path $DEV_REPO_ROOT 'go.mod') -Pattern '^go (\S+)').Matches[0].Groups[1].Value
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        log_error "Go is not installed. Install Go $want or newer: https://go.dev/dl/"
        exit 1
    }
}

function Project-Install {
    Invoke-Native git @('submodule', 'update', '--init', '--recursive')
    Assert-Go
    Invoke-Native go @('mod', 'download')
}

function Project-Build {
    Assert-Go
    $version = (Get-Content (Join-Path $DEV_REPO_ROOT 'VERSION') -Raw).Trim()
    $pkg = 'github.com/nikolareljin/agentvault/cmd'
    Invoke-Native go @('build', '-ldflags', "-s -w -X $pkg.Version=$version", '-o', 'agentvault.exe', '.')
}

function Project-Run {
    Assert-Go
    Invoke-Native go (@('run', '.') + $DEV_ARGS)
}

function Project-Test {
    Assert-Go
    Invoke-Native go @('test', './...')
}

function Project-Lint {
    Assert-Go
    $unformatted = & gofmt -l .
    if ($unformatted) { throw "not gofmt-formatted: $unformatted" }
    Invoke-Native go @('vet', './...')
}

function Project-Clean {
    Remove-Item -ErrorAction SilentlyContinue -Force agentvault.exe, agentvault
}

function Project-Deploy {
    Not-Applicable 'deploy' 'a CLI; releases ship from the release workflow'
}
