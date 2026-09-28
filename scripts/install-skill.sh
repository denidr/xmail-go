#!/usr/bin/env bash
#
# install-skill.sh - install this repo's "xmail" skill via the Vercel `skills` CLI.
#
# The heavy lifting is done by `npx skills` (https://github.com/vercel-labs/skills):
# it knows the skills directory for 78+ agents, auto-detects the agents installed on
# this machine, and installs project-wide (default) or globally (-g). This script
# only pins the source to this repo and the skill name to "xmail", then forwards
# every other argument straight to `npx skills add`.
#
# Examples:
#   bash scripts/install-skill.sh                                  # interactive
#   bash scripts/install-skill.sh -a claude-code -a command-code -a universal
#   bash scripts/install-skill.sh -g -y --copy                     # global, silent
#   bash scripts/install-skill.sh --list                           # list only
#
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
skill_dir="$script_dir/xmail"

# Git Bash and WSL may resolve npx to Windows-native Node, which cannot read POSIX paths.
src="$skill_dir"
if command -v cygpath >/dev/null 2>&1; then
  src="$(cygpath -w "$skill_dir")"
elif [[ -n "${WSL_INTEROP:-}" || -n "${WSL_DISTRO_NAME:-}" ]] &&
  command -v wslpath >/dev/null 2>&1; then
  npx_path="$(command -v npx)"
  case "$npx_path" in
    /mnt/[[:alpha:]]/*|*.exe)
      src="$(wslpath -w "$skill_dir")"
      ;;
  esac
fi

exec npx -y skills add "$src" -s xmail "$@"
