#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('agent','receiver')]
    [string]$Component
)

$ErrorActionPreference = 'Stop'
$name = if ($Component -eq 'agent') { 'AegisAgent' } else { 'AegisReceiver' }
$service = Get-Service -Name $name -ErrorAction Stop
if ($service.Status -ne 'Stopped') {
    Stop-Service -Name $name -ErrorAction Stop
    $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(45))
}
& sc.exe delete $name | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw 'SCM failed to delete the service.'
}
Write-Host "$name unregistered. Aegis config, credentials and spool were NOT deleted."
