$ErrorActionPreference = "Stop"
$repo = if ($env:OX_AGENT_REPOSITORY) { $env:OX_AGENT_REPOSITORY } else { "oxidean/Ox-Agent" }
$version = if ($env:OX_AGENT_VERSION) { $env:OX_AGENT_VERSION } else { (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name }
$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { throw "Unsupported architecture" }
$archive = "ox-agent_$($version.TrimStart('v'))_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/$version"
$temp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
try {
  Invoke-WebRequest "$base/$archive" -OutFile "$temp/$archive"
  Invoke-WebRequest "$base/checksums.txt" -OutFile "$temp/checksums.txt"
  $expected = (Get-Content "$temp/checksums.txt" | Where-Object { $_ -match [regex]::Escape($archive) }).Split()[0]
  $actual = (Get-FileHash "$temp/$archive" -Algorithm SHA256).Hash.ToLower()
  if ($expected -ne $actual) { throw "Checksum verification failed" }
  $installDir = Join-Path $env:LOCALAPPDATA "OxAgent\bin"
  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  Expand-Archive "$temp/$archive" -DestinationPath $temp -Force
  Copy-Item "$temp/ox-agent.exe" (Join-Path $installDir "ox-agent.exe") -Force
  Write-Host "Installed ox-agent $version to $installDir"
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (($userPath -split ';') -notcontains $installDir) {
    $newPath = if ([string]::IsNullOrEmpty($userPath)) { $installDir } else { "$userPath;$installDir" }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $env:Path = "$env:Path;$installDir"
    Write-Host "Added $installDir to the user PATH"
  }
} finally { Remove-Item $temp -Recurse -Force -ErrorAction SilentlyContinue }
