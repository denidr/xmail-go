<#
.SYNOPSIS
Install this repo's "xmail" skill via the Vercel `skills` CLI.

.DESCRIPTION
The heavy lifting is done by `npx skills` (https://github.com/vercel-labs/skills):
it knows the skills directory for 78+ agents, auto-detects the agents installed on
this machine, and installs project-wide (default) or globally (-Global). This script
only pins the source to this repo and the skill name to "xmail".

.PARAMETER Agent
Target agent id(s), e.g. claude-code, command-code, universal. Repeatable. When
omitted, `skills` auto-detects the installed agents (or prompts).

.PARAMETER Global
Install to the user directory (~/) instead of the project.

.PARAMETER Yes
Skip all confirmation prompts.

.PARAMETER Copy
Copy files instead of symlinking into each agent directory.

.PARAMETER List
List the repo's skills without installing.

.EXAMPLE
pwsh -File scripts/install-skill.ps1

.EXAMPLE
pwsh -File scripts/install-skill.ps1 -Agent claude-code -Agent command-code -Agent universal

.EXAMPLE
pwsh -File scripts/install-skill.ps1 -Global -Yes -Copy

.EXAMPLE
pwsh -File scripts/install-skill.ps1 -List
#>
[CmdletBinding()]
param(
    [string[]]$Agent,
    [switch]$Global,
    [switch]$Yes,
    [switch]$Copy,
    [switch]$List
)

$ErrorActionPreference = "Stop"

$skillDir = Join-Path $PSScriptRoot 'xmail'
$npxArgs = @('-y', 'skills', 'add', $skillDir, '-s', 'xmail')
foreach ($a in $Agent) { $npxArgs += @('-a', $a) }
if ($Global) { $npxArgs += '-g' }
if ($Yes) { $npxArgs += '-y' }
if ($Copy) { $npxArgs += '--copy' }
if ($List) { $npxArgs += '--list' }

& npx @npxArgs
exit $LASTEXITCODE
