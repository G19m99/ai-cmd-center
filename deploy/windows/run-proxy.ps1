# Supervisor for AI Command Center on Windows.
#
# Runs ai-cmd-center.exe with config.yaml from InstallDir, waits for it to exit,
# logs the exit code with a timestamp to InstallDir\run-proxy.log, waits 15 seconds
# and starts it again. This covers a bind failure at logon (e.g. Tailscale has no
# IP yet) and any later crash. Task Scheduler's own restart setting only covers a
# failed launch, not the program exiting.
#
# install-task.ps1 registers a logon task that runs this script hidden. To run it by hand:
#   powershell -NoProfile -ExecutionPolicy Bypass -File run-proxy.ps1 -InstallDir C:\ai-cmd-center
# The server's own output of its latest run goes to proxy.out.log / proxy.err.log.
# To stop everything:
#   End-ScheduledTask -TaskName 'AI Command Center'; Get-Process ai-cmd-center -ErrorAction SilentlyContinue | Stop-Process
param(
    [string]$InstallDir = $PSScriptRoot,
    [string]$ConfigPath = '',
    [int]$RestartDelaySeconds = 15
)
$ErrorActionPreference = 'Stop'

$exe = Join-Path $InstallDir 'ai-cmd-center.exe'
if (-not $ConfigPath) { $ConfigPath = Join-Path $InstallDir 'config.yaml' }
$log = Join-Path $InstallDir 'run-proxy.log'

function Write-Log([string]$message) {
    $line = '{0}  {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $message
    Add-Content -Path $log -Value $line -Encoding utf8
}

Write-Log "supervisor started (pid $PID): $exe -config $ConfigPath"
$proc = $null
try {
    while ($true) {
        try {
            $proc = Start-Process -FilePath $exe -ArgumentList @('-config', "`"$ConfigPath`"") `
                -WorkingDirectory $InstallDir -WindowStyle Hidden -PassThru `
                -RedirectStandardOutput (Join-Path $InstallDir 'proxy.out.log') `
                -RedirectStandardError (Join-Path $InstallDir 'proxy.err.log')
            $null = $proc.Handle  # keep the handle open so ExitCode is readable after exit
            Write-Log "started ai-cmd-center (pid $($proc.Id))"
            $proc.WaitForExit()
            Write-Log "ai-cmd-center exited with code $($proc.ExitCode); restarting in $RestartDelaySeconds s"
        } catch {
            Write-Log "failed to start ai-cmd-center: $($_.Exception.Message); retrying in $RestartDelaySeconds s"
        }
        $proc = $null
        Start-Sleep -Seconds $RestartDelaySeconds
    }
} finally {
    # Runs on Ctrl+C. A hard kill of this script (e.g. End-ScheduledTask) skips it,
    # so stop ai-cmd-center.exe separately in that case (see the header).
    if ($proc -and -not $proc.HasExited) {
        Write-Log "supervisor stopping; killing ai-cmd-center (pid $($proc.Id))"
        $proc.Kill()
    }
}
