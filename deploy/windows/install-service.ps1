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
    [System.Management.Automation.PSCredential]$Credential
)

$ErrorActionPreference = 'Stop'
$name = if ($Component -eq 'agent') { 'AegisAgent' } else { 'AegisReceiver' }
$exe = (Resolve-Path -LiteralPath $Executable -ErrorAction Stop).ProviderPath
$cfg = (Resolve-Path -LiteralPath $Config -ErrorAction Stop).ProviderPath

foreach ($value in @($exe, $cfg)) {
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

$command = '"' + $exe + '" start --config "' + $cfg + '" --component ' + $Component
New-Service -Name $name -BinaryPathName $command -StartupType Automatic -Credential $Credential -Description "Aegis telemetry $Component"
& sc.exe failure $name reset= 86400 actions= restart/5000/restart/15000/restart/60000 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Warning 'Service registered, but recovery actions could not be configured. Fix the service manually.'
    throw 'SCM recovery configuration failed.'
}
Write-Host "$name installed, but not started. Verify service-account access to config, secrets and spool, then Start-Service $name."
