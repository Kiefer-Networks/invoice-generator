$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Push-Location (Split-Path -Parent $PSScriptRoot)
function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command failed: exit $LASTEXITCODE" }
}
$compose = @('compose', '-p', 'invoice-ci', '-f', 'compose.yaml', '-f', 'compose.dev.yaml')
try {
    & ./scripts/test.ps1
    Write-Host 'Production build and isolation'
    Invoke-Checked go @('build', '-tags=production', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -buildid=', '-o', 'NUL', './cmd/server')
    Invoke-Checked go @('test', './cmd/server', '-run', '^TestProductionBinaryExcludesDevelopment$', '-count=1')
    Write-Host 'Compose configuration'
    Invoke-Checked docker @('compose', '-f', 'compose.yaml', 'config', '--quiet')
    Invoke-Checked docker ($compose + @('config', '--quiet'))
    Write-Host 'Production container'
    Invoke-Checked docker @('build', '--target', 'production', '-t', 'invoice-generator:test', '.')
    $env:INVOICE_CONTAINER_TEST = '1'
    Invoke-Checked go @('test', './cmd/server', '-run', '^TestContainerRuntime$', '-count=1', '-v')
    Remove-Item Env:INVOICE_CONTAINER_TEST
    Write-Host 'Compose browser and PDF smoke'
    Invoke-Checked docker ($compose + @('build'))
    Invoke-Checked docker ($compose + @('up', '-d', '--wait', '--wait-timeout', '180'))
    Invoke-Checked docker ($compose + @('exec', '-T', 'invoice', '/usr/local/bin/healthcheck'))
    $env:INVOICE_COMPOSE_TEST = 'running'
    Invoke-Checked go @('test', './cmd/server', '-run', '^TestComposeRuntimeState$', '-count=1')
    Invoke-Checked docker ($compose + @('run', '--rm', '--no-deps', '--entrypoint', '/usr/local/bin/browser.test', 'invoice', '-test.run', '^TestBrowserWorkflow$', '-test.v', '-test.timeout', '5m'))
    Invoke-Checked docker ($compose + @('run', '--rm', '--no-deps', '-e', 'INVOICE_BROWSER_BASE_URL=http://127.0.0.1:8080', '--entrypoint', '/usr/local/bin/browser.test', 'invoice', '-test.run=^TestComposeBrowserWorkflow$', '-test.v', '-test.timeout=5m'))
    Invoke-Checked docker ($compose + @('restart', 'invoice'))
    Invoke-Checked docker ($compose + @('up', '-d', '--wait', '--wait-timeout', '180'))
    Invoke-Checked docker ($compose + @('run', '--rm', '--no-deps', '-e', 'INVOICE_BROWSER_BASE_URL=http://127.0.0.1:8080', '--entrypoint', '/usr/local/bin/browser.test', 'invoice', '-test.run=^TestComposeBrowserPersistence$', '-test.v', '-test.timeout=5m'))
    Invoke-Checked docker ($compose + @('stop', '-t', '35'))
    $env:INVOICE_COMPOSE_TEST = 'stopped'
    Invoke-Checked go @('test', './cmd/server', '-run', '^TestComposeRuntimeState$', '-count=1')
    $env:INVOICE_CONTAINER_TEST = '1'
    Invoke-Checked go @('test', './cmd/server', '-run', '^TestContainerRecoveryDrill$', '-count=1', '-v')
} finally {
    Remove-Item Env:INVOICE_CONTAINER_TEST -ErrorAction SilentlyContinue
    Remove-Item Env:INVOICE_COMPOSE_TEST -ErrorAction SilentlyContinue
    & docker @compose down
    $cleanupExit = $LASTEXITCODE
    Pop-Location
    if ($cleanupExit -ne 0) { throw "Compose teardown failed: exit $cleanupExit" }
}
