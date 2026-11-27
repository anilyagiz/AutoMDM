# ----- USER SETTINGS -----
# FOR GUI USAGE:
# Add your secret (with AUTOMDM_SECRET_ prefix) to AutoMDM Desktop's Controls > Variables
# Example: If you create a variable named "API", it becomes AUTOMDM_SECRET_API
# Then update the variable name in the line below to match your AutoMDM secret name
# WARNING: AutoMDM will fail to upload this script if the variable name doesn't exist in your AutoMDM secrets
# FOR GITOPS USAGE: 
# Add your GitHub secret to the workflow env section (see AutoMDM guide for details)
# Example: AUTOMDM_SECRET_API: ${{ secrets.AUTOMDM_API_TOKEN }}
# GitOps will automatically upload the variable to AutoMDM when syncing
# 
# For complete documentation on AutoMDM variables, see:
# https://automdm.com/guides/secrets-in-scripts-and-configuration-profiles

$NODE_NAME = "OKTA"                    
# Edit this to match your CSP node name

$AUTOMDM_API = "$AUTOMDM_SECRET_API"
# Update this to match your AutoMDM secret name
# -------------------------

$CmdId = [System.DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
Write-Host "Current Date and Time (UTC - YYYY-MM-DD HH:MM:SS formatted): $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"
Write-Host "AutoMDM URL: $env:AUTOMDM_DESKTOP_AUTOMDM_URL"

try {
    $HostUUID = (Get-CimInstance Win32_ComputerSystemProduct).UUID
    Write-Host "Host UUID: $HostUUID"
} catch {
    $HostUUID = (Get-WmiObject Win32_ComputerSystemProduct).UUID
    Write-Host "Host UUID (via WMI): $HostUUID"
}

Write-Host "Command ID: $CmdId"
Write-Host "Triggering SCEP enrollment..."

$SyncML = @"
<Exec>
  <CmdID>$CmdId</CmdID>
  <Item>
    <Target>
      <LocURI>./Device/Vendor/MSFT/ClientCertificateInstall/SCEP/$NODE_NAME/Install/Enroll</LocURI>
    </Target>
    <Meta>
      <Format xmlns="syncml:metinf">null</Format>
      <Type>text/plain</Type>
    </Meta>
    <Data></Data>
  </Item>
</Exec>
"@

$EncodedCommand = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($SyncML))

$Body = @{
    command = $EncodedCommand
    host_uuids = @($HostUUID)
} | ConvertTo-Json

Write-Host "Sending MDM command to host: $HostUUID"

try {
    $Response = Invoke-RestMethod -Uri "$env:AUTOMDM_DESKTOP_AUTOMDM_URL/api/v1/automdm/commands/run" `
        -Method POST `
        -Headers @{"Authorization"="Bearer $AUTOMDM_API";"Content-Type"="application/json"} `
        -Body $Body
    $CommandUUID = $Response.command_uuid
    Write-Host "PASS - SCEP enrollment command sent successfully!"
    Write-Host "Command UUID: $CommandUUID"
    Write-Host ""
    Write-Host "To check results, copy and paste this command:"
    Write-Host "automdmctl get mdm-command-results --id=$CommandUUID"
}
catch {
    Write-Host "FAIL - SCEP enrollment failed: $($_.Exception.Message)"
    if ($_.ErrorDetails) {
        Write-Host "Error Details: $($_.ErrorDetails.Message)"
    }
}
