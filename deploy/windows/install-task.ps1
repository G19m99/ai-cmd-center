# Registers a Task Scheduler task that starts AI Command Center when you log on.
#
# Usage (from an elevated or normal PowerShell; normal is enough for a per-user task):
#   powershell -ExecutionPolicy Bypass -File deploy\windows\install-task.ps1 -InstallDir C:\ai-cmd-center
#
# InstallDir must contain ai-cmd-center.exe, config.yaml and static\management.html
# (copy the contents of dist\ there and create config.yaml from config.example.yaml).
#
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

$action = New-ScheduledTaskAction -Execute $exe -Argument "-config `"$config`"" -WorkingDirectory $InstallDir
# Start at logon of the current user. Tailscale may still be connecting, so wait a little.
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$trigger.Delay = 'PT30S'
# Restart on failure (e.g. the Tailscale IP was not up yet and the bind failed).
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
