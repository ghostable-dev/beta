//go:build windows

package userpresence

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"
)

func verifyPlatformUserPresence(request Request) error {
	powershellPath, err := windowsPowerShellPath()
	if err != nil {
		return err
	}

	cmd := exec.Command(powershellPath,
		"-NoProfile",
		"-ExecutionPolicy",
		"Bypass",
		"-EncodedCommand",
		encodePowerShellCommand(windowsUserConsentScript(confirmationMessage(request))),
	)
	cmd.Stdin = request.In
	cmd.Stdout = request.Out
	cmd.Stderr = request.ErrOut
	return cmd.Run()
}

func windowsPowerShellPath() (string, error) {
	for _, name := range []string{"powershell.exe", "pwsh.exe"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("PowerShell was not found; install PowerShell or use a scoped GHOSTABLE_CI_TOKEN automation credential")
}

func encodePowerShellCommand(script string) string {
	encoded := utf16.Encode([]rune(script))
	bytes := make([]byte, 0, len(encoded)*2)
	for _, value := range encoded {
		bytes = append(bytes, byte(value), byte(value>>8))
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func windowsUserConsentScript(message string) string {
	quotedMessage := "'" + strings.ReplaceAll(message, "'", "''") + "'"
	return `
$ErrorActionPreference = 'Stop'
try {
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    $null = [Windows.Security.Credentials.UI.UserConsentVerifier, Windows.Security.Credentials.UI, ContentType = WindowsRuntime]
    $operation = [Windows.Security.Credentials.UI.UserConsentVerifier]::RequestVerificationAsync(` + quotedMessage + `)
    $method = [System.WindowsRuntimeSystemExtensions].GetMethods() |
        Where-Object { $_.Name -eq 'AsTask' -and $_.IsGenericMethodDefinition -and $_.GetParameters().Count -eq 1 } |
        Select-Object -First 1
    if ($null -eq $method) {
        Write-Error 'Windows Runtime task bridge is unavailable.'
        exit 1
    }
    $task = $method.MakeGenericMethod([Windows.Security.Credentials.UI.UserConsentVerificationResult]).Invoke($null, @($operation))
    $result = $task.GetAwaiter().GetResult()
    if ($result -eq [Windows.Security.Credentials.UI.UserConsentVerificationResult]::Verified) {
        exit 0
    }
    Write-Error "Windows user verification returned $result."
    exit 1
} catch {
    Write-Error $_
    exit 1
}
`
}
