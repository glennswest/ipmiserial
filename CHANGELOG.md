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
- **fix:** Replace block dedup with line-level dedup — keeps set of last 200 lines, suppresses screen-redraw duplicates from Dell iDRAC cursor repositioning
- **fix:** SSE dedup event for live "(Duplicated N lines)" display in xterm.js
- **fix:** SSE reconnect on browser tab visibility change
- **fix:** Enforce 2-minute rotation cooldown — prevents duplicate pxemanager calls from splitting boot logs mid-BIOS
- **fix:** Strip orphaned DEC private mode ([=3h) and incomplete ANSI fragments from logs

### 2026-02-24
- **fix:** SSE live view broken during Fedora PXE install — `containsRow1Cursor` matched generic `\x1b[H` (cursor home) used by systemd/dracut/Anaconda, causing constant screen clearing; now only matches BIOS-specific `\x1b[01;00H`
- **fix:** Log cleaner: strip mid-row cursor positions instead of converting to newlines (prevents fragments like `<F1>` appearing as separate lines)
- **fix:** Analytics: strip ANSI escape codes before pattern matching — embedded color codes in systemd/Fedora output were breaking regex matches
- **feat:** Analytics: detect Fedora installer (Anaconda), `dracut` switching root, and broader `Fedora \d+` version matching
- **fix:** SSE reconnect no longer clears terminal — skip catchup and clear screen when switching tabs (terminal already has content)
- **feat:** Boot timeline milestones in analytics — tracks iPXE init, kernel/initramfs download, GRUB boot (with count for reboots), SSH ready, and login ready with elapsed timestamps
