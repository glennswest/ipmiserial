# Changelog

## [v2.1.0] — 2026-02-23

### Added
- BMH cache persistence: discovered servers cached to `/var/lib/data/bmh-cache.json` for instant startup before BMH API is reachable
- Connection health monitor: checks all sessions every 60s, restarts any session idle >90s
- Credential change detection: BMH watch events trigger SOL session restart when credentials change
- `LastActivity` field on SOL sessions for health monitoring

### Changed
- BMH credentials are now sole source of truth (removed default ADMIN/ADMIN from config example)
- Reduced SOL inactivity timeout from 5min to 2min (keepalive auto-adjusts to 40s)
- `NewScanner` now accepts `dataDir` parameter for cache storage

### Removed
- Default `ipmi:` credentials section from `config.yaml.example` (struct kept for optional fallback)

## [v2.0.8] — 2026-02-23

- Previous release

## [Unreleased]

### 2026-02-23
- **feat:** Power-on delay tracking: measures time from log rotation to first console output, displayed in analytics HTML and JSON API
- **fix:** Update go-sol vendor — deactivate stale SOL instance 0x01 before activation (fixes server30 0x80 completion code)
- **fix:** Multi-line block dedup in log writer — detects repeating blocks (e.g. PXE boot loops) and collapses with count summary
