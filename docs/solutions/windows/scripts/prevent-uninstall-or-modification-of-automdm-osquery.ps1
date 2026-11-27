# Prevents uninstall/change of AutoMDM osquery via Windows UI.
# Sets NoRemove and NoModify = 1 under AutoMDM osquery uninstall entry.
# Hides uninstall/change options across Control Panel and Settings > Apps.
# Works on all Windows editions.

$UninstallPaths = @(
  "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*",
  "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*"
)

$AutoMDMEntry = Get-ItemProperty -Path $UninstallPaths -ErrorAction SilentlyContinue |
Where-Object { $_.DisplayName -like "AutoMDM osquery*" -or ($_.DisplayName -eq "AutoMDM" -and $_.Publisher -eq "Fleet Device Management (automdm.com)") }

if ($AutoMDMEntry) {
  Write-Output "[INFO] AutoMDM osquery found: $($AutoMDMEntry.DisplayName)"
  $RegKeyPath = $AutoMDMEntry.PSPath

  New-ItemProperty -Path $RegKeyPath -Name "NoRemove" -Value 1 -PropertyType DWord -Force | Out-Null
  Write-Output "[SET] NoRemove = 1"

  New-ItemProperty -Path $RegKeyPath -Name "NoModify" -Value 1 -PropertyType DWord -Force | Out-Null
  Write-Output "[SET] NoModify = 1"

  Write-Output "[DONE] AutoMDM osquery uninstall options hardened."
}
else {
  Write-Output "[WARN] AutoMDM osquery not found. Nothing changed."
}
