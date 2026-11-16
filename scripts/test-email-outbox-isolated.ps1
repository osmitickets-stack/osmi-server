param()
$ErrorActionPreference = 'Stop'
$serverRoot = Split-Path -Parent $PSScriptRoot
$dbRoot = Join-Path (Split-Path -Parent $serverRoot) 'osmi-db-email-delivery'
$migrations = Join-Path $dbRoot 'migrations'
$container = 'osmi-checkin-test-postgres-test-1'
$previousGoWork = [Environment]::GetEnvironmentVariable('GOWORK', 'Process')
$previousTestURL = [Environment]::GetEnvironmentVariable('OSMI_TEST_DATABASE_URL', 'Process')

try {
    if (-not (Test-Path -LiteralPath (Join-Path $migrations '000014_ticket_email_outbox.up.sql'))) {
        throw 'MIGRACION_14_NO_ENCONTRADA'
    }
    $preflight = & docker exec $container psql -X -w -U osmi -d osmidb_test -v ON_ERROR_STOP=1 -At -F '|' -c "SELECT current_database(), (SELECT version FROM public.schema_migrations), (SELECT dirty FROM public.schema_migrations), (SELECT marker FROM public.osmi_checkin_test_guard LIMIT 1)"
    if ($LASTEXITCODE -ne 0 -or $preflight -notin @('osmidb_test|13|f|osmi-checkin-isolated-v1', 'osmidb_test|14|f|osmi-checkin-isolated-v1')) {
        throw 'TEST_DB_PREFLIGHT_FAILED'
    }
    $networkJson = & docker inspect --format '{{json .NetworkSettings.Networks}}' $container
    if ($LASTEXITCODE -ne 0) { throw 'TEST_DB_NETWORK_INSPECT_FAILED' }
    $networks = $networkJson | ConvertFrom-Json
    $names = @($networks.PSObject.Properties.Name)
    if ($names.Count -ne 1) { throw 'TEST_DB_NETWORK_AMBIGUOUS' }
    $secure = Read-Host 'Contraseña PostgreSQL SOLO de osmidb_test' -AsSecureString
    $plain = [System.Net.NetworkCredential]::new('', $secure).Password
    $url = 'postgres://osmi:' + [uri]::EscapeDataString($plain) + '@postgres-test:5432/osmidb_test?sslmode=disable'
    $env:OSMI_TEST_DATABASE_URL = 'postgres://osmi:' + [uri]::EscapeDataString($plain) + '@127.0.0.1:55439/osmidb_test?sslmode=disable'
    $env:GOWORK = 'off'
    Remove-Variable plain,secure -ErrorAction SilentlyContinue
    if ($preflight -eq 'osmidb_test|13|f|osmi-checkin-isolated-v1') {
        & docker run --rm --network $names[0] --mount "type=bind,source=$migrations,target=/migrations,readonly" migrate/migrate:v4.17.0 -path=/migrations "-database=$url" up 1
        if ($LASTEXITCODE -ne 0) { throw 'TEST_MIGRATION_14_FAILED' }
    }
    Remove-Variable url -ErrorAction SilentlyContinue
    $postflight = & docker exec $container psql -X -w -U osmi -d osmidb_test -v ON_ERROR_STOP=1 -At -F '|' -c "SELECT current_database(), (SELECT version FROM public.schema_migrations), (SELECT dirty FROM public.schema_migrations), to_regclass('notifications.ticket_email_jobs') IS NOT NULL"
    if ($LASTEXITCODE -ne 0 -or $postflight -ne 'osmidb_test|14|f|t') {
        throw 'TEST_MIGRATION_14_POSTCHECK_FAILED'
    }
    Write-Output 'TEST_MIGRATION_14_PASS'
    Push-Location $serverRoot
    try {
        & go test -count=1 -v -timeout 180s ./internal/application/services ./internal/application/handlers/grpc ./cmd/worker ./internal/maintenance ./internal/infrastructure/email
        if ($LASTEXITCODE -ne 0) { throw 'EMAIL_OUTBOX_ISOLATED_TESTS_FAILED' }
    } finally {
        Pop-Location
    }
    Write-Output 'EMAIL_OUTBOX_ISOLATED_PASS'
} finally {
    if ($null -eq $previousTestURL) { Remove-Item Env:\OSMI_TEST_DATABASE_URL -ErrorAction SilentlyContinue } else { $env:OSMI_TEST_DATABASE_URL = $previousTestURL }
    if ($null -eq $previousGoWork) { Remove-Item Env:\GOWORK -ErrorAction SilentlyContinue } else { $env:GOWORK = $previousGoWork }
    Remove-Variable url,plain,secure,previousTestURL,previousGoWork -ErrorAction SilentlyContinue
}
