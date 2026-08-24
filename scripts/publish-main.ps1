$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$env:GH_CONFIG_DIR = Join-Path $env:APPDATA 'GitHub CLI'
$env:GIT_CONFIG_COUNT = '1'
$env:GIT_CONFIG_KEY_0 = 'safe.directory'
$env:GIT_CONFIG_VALUE_0 = $repoRoot.Replace('\', '/')
$logPath = Join-Path $repoRoot '.cache\publish-main.log'
New-Item -ItemType Directory -Force (Split-Path -Parent $logPath) | Out-Null
Start-Transcript -Path $logPath -Force | Out-Null

Push-Location $repoRoot
try {
	$owner = (gh api user --jq .login).Trim()
	if (-not $owner) { throw 'Could not determine the authenticated GitHub account.' }
	$repository = "$owner/aipool"
    git add .dockerignore .gitignore Dockerfile README.md go.mod compose.yaml cmd internal scripts
    git diff --cached --check
    git status --short
    git diff --cached --quiet
    if ($LASTEXITCODE -ne 0) {
        git commit -m 'Initial AIPool GPU inference prototype'
    }

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
	gh repo view $repository --json nameWithOwner 2>$null | Out-Null
    $repositoryExists = $LASTEXITCODE -eq 0
    $ErrorActionPreference = $previousPreference
    if (-not $repositoryExists) {
		gh repo create $repository --public --description 'Self-hosted shared AI inference pool'
	}

	$remote = "https://github.com/$repository.git"
    $ErrorActionPreference = 'Continue'
    $existingRemote = git remote get-url origin 2>$null
    $remoteExists = $LASTEXITCODE -eq 0
    $ErrorActionPreference = $previousPreference
    if (-not $remoteExists) {
        git remote add origin $remote
    } elseif ($existingRemote -ne $remote) {
        throw "origin points to unexpected remote: $existingRemote"
    }

    git push -u origin main
    git status -sb
	gh repo view $repository --json nameWithOwner,visibility,defaultBranchRef,url
} catch {
    Write-Error $_
    exit 1
} finally {
    Pop-Location
    Stop-Transcript | Out-Null
}
