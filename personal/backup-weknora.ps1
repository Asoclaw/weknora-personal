param(
    [int]$Keep = 7
)

$ErrorActionPreference = 'Stop'
if ($Keep -lt 1) { throw 'Keep must be at least 1' }
$workspace = Split-Path -Parent $PSScriptRoot
$backupRoot = Join-Path $workspace 'data\backups'
New-Item -ItemType Directory -Path $backupRoot -Force | Out-Null
$backupDir = Join-Path $backupRoot ('backup-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Path $backupDir -Force | Out-Null

$databaseFile = Join-Path $backupDir 'postgres.dump'
$filesArchive = Join-Path $backupDir 'files.tar.gz'
$containerDump = '/tmp/personal-rag-backup.dump'
try {
    docker exec WeKnora-postgres sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /tmp/personal-rag-backup.dump'
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL backup failed' }
    docker cp "WeKnora-postgres:${containerDump}" $databaseFile
    if ($LASTEXITCODE -ne 0) { throw 'Could not copy PostgreSQL backup' }

    $dockerBackupPath = $backupDir -replace '\\', '/'
    docker run --rm --entrypoint tar -v 'weknora_data-files:/source:ro' -v "${dockerBackupPath}:/backup" wechatopenai/weknora-ui:v0.8.0 -czf /backup/files.tar.gz -C /source .
    if ($LASTEXITCODE -ne 0) { throw 'File archive failed' }

    $manifest = [ordered]@{
        created_at = (Get-Date).ToUniversalTime().ToString('o')
        source = 'WeKnora production containers'
        postgres_sha256 = (Get-FileHash -LiteralPath $databaseFile -Algorithm SHA256).Hash
        files_sha256 = (Get-FileHash -LiteralPath $filesArchive -Algorithm SHA256).Hash
    }
    $manifest | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $backupDir 'manifest.json') -Encoding utf8
} finally {
    docker exec WeKnora-postgres rm -f $containerDump 2>$null | Out-Null
}

$resolvedRoot = (Resolve-Path -LiteralPath $backupRoot).Path.TrimEnd('\')
$old = @(Get-ChildItem -LiteralPath $backupRoot -Directory | Where-Object { $_.Name -match '^backup-\d{8}-\d{6}$' } | Sort-Object Name -Descending | Select-Object -Skip $Keep)
foreach ($entry in $old) {
    $resolved = (Resolve-Path -LiteralPath $entry.FullName).Path
    if (-not $resolved.StartsWith($resolvedRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove backup outside $resolvedRoot"
    }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
Write-Output "Backup created: $backupDir"
