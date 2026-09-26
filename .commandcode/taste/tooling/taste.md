# Taste — Tooling & Stack

- Prefers Go for backend services that must be as lightweight as possible (explicitly chosen over Python/JS after comparison). Confidence: 0.8
- Optimizes for a small footprint / few dependencies ("seringan mungkin") when choosing stack and storage. Confidence: 0.8
- Prefers local/temporary persistence in SQLite for simple service state. Confidence: 0.6
- Requires build/release tooling that runs on Windows as well as Linux — no `.sh`-only scripts; cross-platform release scripts are expected. Confidence: 0.8
- Wants release artifacts for multiple targets: Docker images (x64 and arm64/armbian) and Windows x64 as a service with a system tray app. Confidence: 0.7
- Wants MCP support as a first-class interface alongside the HTTP API, plus agent-facing skill artifacts for using the service over HTTP and MCP. Confidence: 0.75
- Prefers credentials and environment-specific values externalized as configurable parameters rather than hardcoded. Confidence: 0.8
- Keeps auto-generated/local agent tooling state (e.g. `.commandcode/`) out of git commits — stages project changes while leaving that directory untracked. Confidence: 0.55
- Wants a single, dedicated dev verification script ("script run test untuk dev") that runs the whole pre-commit gate in order — format check, module-tidy diff, vet, build, unit + integration tests, cross-target compile check — and fails fast on the first failure; it should work standalone (CI) without depending on `make`, with Makefile targets as thin aliases. Confidence: 0.7
- Expects script tooling to be maintained in cross-platform parity — every shell script has an equivalent native-PowerShell version (release as well as test scripts) so bare Windows (no Git Bash/WSL) is a first-class path. Confidence: 0.8
