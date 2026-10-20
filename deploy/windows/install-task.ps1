# Registers a Task Scheduler task that starts AI Command Center when you log on.
# The task runs run-proxy.ps1 (copied into InstallDir) in a hidden PowerShell window.
# That script restarts the server whenever it exits, e.g. when the bind fails because
# Tailscale has no IP yet. Task Scheduler's restart setting only covers a failed launch.
#
# Usage (from an elevated or normal PowerShell; normal is enough for a per-user task):
#   powershell -ExecutionPolicy Bypass -File deploy\windows\install-task.ps1 -InstallDir C:\ai-cmd-center
#
# InstallDir must contain ai-cmd-center.exe, config.yaml and static\management.html
# (copy the contents of dist\ there and create config.yaml from config.example.yaml).
#
# Stop it with:
#   End-ScheduledTask -TaskName 'AI Command Center'; Get-Process ai-cmd-center -ErrorAction SilentlyContinue | Stop-Process
# Remove the task with:
#   Unregister-ScheduledTask -TaskName 'AI Command Center' -Confirm:$false
param(
    [Parameter(Mandatory = $true)]
    [string]$InstallDir,
    [string]$TaskName = 'AI Command Center'
)
$ErrorActionPreference = 'Stop'

$InstallDir = (Resolve-Path $InstallDir).Path
$exe = Join-Path $InstallDir 'ai-cmd-center.exe'
$config = Join-Path $InstallDir 'config.yaml'

foreach ($path in $exe, $config, (Join-Path $InstallDir 'static\management.html')) {
    if (-not (Test-Path $path)) { throw "Missing $path" }
}

# Copy the supervisor next to the exe so the task doesn't depend on the repo checkout.
$wrapper = Join-Path $InstallDir 'run-proxy.ps1'
$wrapperSource = Join-Path $PSScriptRoot 'run-proxy.ps1'
if ((Resolve-Path $wrapperSource).Path -ne $wrapper) {
    Copy-Item -Force $wrapperSource $wrapper
}

# Hidden window: there is no console the user could close by accident.
$powershell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$arguments = "-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$wrapper`" -InstallDir `"$InstallDir`""
$action = New-ScheduledTaskAction -Execute $powershell -Argument $arguments -WorkingDirectory $InstallDir
# Start at logon of the current user. Tailscale may still be connecting, so wait a little.
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$trigger.Delay = 'PT30S'
# run-proxy.ps1 restarts the server when it exits. These settings only restart the
# wrapper itself if Task Scheduler fails to launch it, and remove the 72h time limit.
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -RestartCount 999 `
    -RestartInterval (New-TimeSpan -Minutes 1)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
Write-Host "Registered scheduled task '$TaskName'."
Write-Host "Start it now with:  Start-ScheduledTask -TaskName '$TaskName'"
Write-Host "Supervisor log: $(Join-Path $InstallDir 'run-proxy.log')   Server output: proxy.out.log / proxy.err.log"
