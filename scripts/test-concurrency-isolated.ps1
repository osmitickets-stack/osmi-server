$ErrorActionPreference = 'Stop'
$repository = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $repository

$previousGoWork = [Environment]::GetEnvironmentVariable('GOWORK', 'Process')
$secure = Read-Host 'Contrasena PostgreSQL SOLO de PRUEBAS' -AsSecureString
try {
    $plain = [System.Net.NetworkCredential]::new('', $secure).Password
    $env:OSMI_TEST_DATABASE_URL = 'postgres://osmi:' + [uri]::EscapeDataString($plain) + '@127.0.0.1:55439/osmidb_test?sslmode=disable'
    Remove-Variable plain -ErrorAction SilentlyContinue
    $env:GOWORK = 'off'

    $pattern = '^Test(IntegrationDBGuard|LastTicketConcurrentReservationsIntegration|ConcurrentExpiryPreservesPaidInventoryIntegration|PaymentWinsExpiryRaceIntegration|ExpiryKeepsInFlightPaymentIntegration|IntentCreationAndExpiryRaceIntegration|ProcessPaidOrderConcurrentIsIdempotent|HandleWebhookConcurrentSameEventIsIdempotent|WorkerCancelsBeforeReleasingInventoryIntegration)$'
    $result = & go test -count=1 -v -timeout 180s ./internal/application/services ./cmd/worker -run $pattern 2>&1
    $code = $LASTEXITCODE
    $result | ForEach-Object { Write-Host $_ }
    if ($code -ne 0 -or ($result | Where-Object { $_ -match '^\s*--- SKIP:' })) {
        throw 'CONCURRENCY_ISOLATED_NOT_VALIDATED'
    }
    Write-Host 'CONCURRENCY_ISOLATED_PASS'
}
finally {
    Remove-Item Env:\OSMI_TEST_DATABASE_URL -ErrorAction SilentlyContinue
    if ($null -eq $previousGoWork) {
        Remove-Item Env:\GOWORK -ErrorAction SilentlyContinue
    }
    else {
        $env:GOWORK = $previousGoWork
    }
    Remove-Variable secure, plain, previousGoWork -ErrorAction SilentlyContinue
}
