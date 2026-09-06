$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    Write-Host 'Format'
    $files = @(git ls-files --cached --others --exclude-standard -- '*.go')
    if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate Go sources' }
    $unformatted = @(gofmt -l $files)
    if ($LASTEXITCODE -ne 0 -or $unformatted.Count -ne 0) { throw ('Run gofmt on: ' + ($unformatted -join ', ')) }
    Write-Host 'Vet'
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Vet failed' }
    Write-Host 'Unit'
    go test ./cmd/invoice ./internal/config ./internal/invoicing ./internal/locale ./internal/paperless ./internal/pdfattach ./internal/pdfgen ./internal/render ./internal/units ./internal/zugferd
    if ($LASTEXITCODE -ne 0) { throw 'Unit tests failed' }
    Write-Host 'Integration'
    go test ./cmd/server ./internal/auth ./internal/devmode ./internal/documents ./internal/jobs ./internal/store ./internal/web -skip '^TestBrowserWorkflow$'
    if ($LASTEXITCODE -ne 0) { throw 'Integration tests failed' }
    Write-Host 'Browser'
    go test ./internal/web -run '^TestBrowserWorkflow$' -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw 'Browser tests failed' }
} finally {
    Pop-Location
}
