#!/usr/bin/env pwsh
# Evaluation script: check whether the agent output identified a bug and
# provided a fix suggestion. PowerShell counterpart of judge-debug-eval.sh,
# for use as a script judge on Windows hosts.
$message = $env:EVAL_FINAL_MESSAGE

if ($message -match 'bug') {
    if ($message -match 'fix|nil') {
        Write-Output 'Agent identified the bug AND provided a fix'
        exit 0
    }
    Write-Output 'Agent identified the bug but did NOT provide a fix'
    exit 1
}

Write-Output 'Agent failed to identify any bug'
exit 1
