#Requires -RunAsAdministrator
# A password is accepted as a PowerShell PSCredential, never a command line.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('agent','receiver')]
    [string]$Component,

    [Parameter(Mandatory = $true)]
    [string]$Executable,

    [Parameter(Mandatory = $true)]
    [string]$Config,

    [Parameter(Mandatory = $true)]
    [System.Management.Automation.PSCredential]$Credential,

    [string]$DatabaseURLFile = ''
)

$ErrorActionPreference = 'Stop'
$name = if ($Component -eq 'agent') { 'AegisAgent' } else { 'AegisReceiver' }
$exe = (Resolve-Path -LiteralPath $Executable -ErrorAction Stop).ProviderPath
$cfg = (Resolve-Path -LiteralPath $Config -ErrorAction Stop).ProviderPath
$dbFile = ''
if ($Component -eq 'receiver') {
    if ([string]::IsNullOrWhiteSpace($DatabaseURLFile)) {
        throw 'Receiver requires -DatabaseURLFile pointing to an existing private PostgreSQL URL file.'
    }
    $dbFile = (Resolve-Path -LiteralPath $DatabaseURLFile -ErrorAction Stop).ProviderPath
} elseif ($DatabaseURLFile -ne '') {
    throw '-DatabaseURLFile is only valid for receiver.'
}

foreach ($value in @($exe, $cfg, $dbFile)) {
    if ($value.Contains('"') -or $value.Contains([char]10) -or $value.Contains([char]13)) {
        throw 'Paths must not contain quotation marks or newlines.'
    }
}
if ($Credential.UserName -match '^(NT AUTHORITY\\)?(SYSTEM|LOCAL SERVICE|NETWORK SERVICE)$') {
    throw 'Use a dedicated unprivileged account, not an operating-system account.'
}
if (Get-Service -Name $name -ErrorAction SilentlyContinue) {
    throw "$name already exists; refusing to overwrite an installed service."
}

& $exe validate-config --config $cfg | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw 'Invalid Aegis config; nothing was installed.'
}
$profile = Get-Content -LiteralPath $cfg -Raw | ConvertFrom-Json -ErrorAction Stop
if ($null -eq $profile.PSObject.Properties[$Component] -or $null -eq $profile.$Component) {
    throw "Configuration does not contain the selected $Component profile."
}

$command = '"' + $exe + '" start --config "' + $cfg + '" --component ' + $Component
New-Service -Name $name -BinaryPathName $command -StartupType Automatic -Credential $Credential -Description "Aegis telemetry $Component"
if ($Component -eq 'receiver') {
    # Only a *path* is placed in SCM's per-service environment; no password.
    New-ItemProperty -Path ("HKLM:\SYSTEM\CurrentControlSet\Services\" + $name) `
        -Name Environment -PropertyType MultiString `
        -Value @("AEGIS_DATABASE_URL_FILE=$dbFile") -Force | Out-Null
}
& sc.exe failure $name reset= 86400 actions= restart/5000/restart/15000/restart/60000 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Warning 'Service registered, but recovery actions could not be configured. Fix the service manually.'
    throw 'SCM recovery configuration failed.'
}
# Restart also when the service reports STOPPED with a nonzero exit code.
& sc.exe failureflag $name 1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw 'SCM failed to enable restart after service-reported failures.'
}
Write-Host "$name installed, but not started. Verify service-account access to config, secrets and spool, then Start-Service $name."
