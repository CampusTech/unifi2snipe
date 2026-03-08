# CLAUDE.md

## Project Overview

unifi2snipe is a Go CLI tool that syncs UniFi network devices into Snipe-IT asset management. It uses the UniFi Site Manager API to fetch devices and the Snipe-IT REST API to create/update hardware assets.

## Build & Run

```bash
# Build (private fork requires env vars)
GONOSUMCHECK=github.com/CampusTech/go-snipeit GONOSUMDB=github.com/CampusTech/go-snipeit go build ./...

# Run
go run . sync -v
go run . setup
go run . download
```

## Project Structure

- `main.go` — Entry point, sets version
- `cmd/` — Cobra CLI commands (root, sync, setup, download)
- `config/` — YAML config loading, validation, and merge helpers
- `sync/` — Core sync engine (diff, create, update, checkout logic)
- `snipe/` — Snipe-IT client wrapper with dry-run enforcement
- `unifi/` — UniFi Site Manager API client
- `settings.yaml` — User config (gitignored)
- `settings.example.yaml` — Example config template

## Key Dependencies

- `github.com/CampusTech/go-snipeit` — Snipe-IT Go SDK (CampusTech fork, `merged` branch)
  - Module path uses `github.com/CampusTech/go-snipeit` (not upstream `michellepellon`)
  - Pseudo-version format: `v0.0.0-campustech.0.YYYYMMDDHHMMSS-HASH12`
  - Build requires: `GONOSUMCHECK=github.com/CampusTech/go-snipeit GONOSUMDB=github.com/CampusTech/go-snipeit`
- `github.com/spf13/cobra` — CLI framework
- `github.com/sirupsen/logrus` — Structured logging

## Conventions

- MAC addresses are used as asset serial numbers and asset tags in Snipe-IT
- Custom fields use the `_snipeit_<name>_<id>` column naming convention
- Each package has its own logrus logger (set via SetLogLevel/SetLogFormatter/SetLogOutput)
- Dry-run mode is enforced at the snipe client level (returns ErrDryRun)
- The `setup` command is idempotent — finds existing resources before creating

## Config File

Config is loaded from `settings.yaml` (YAML) with environment variable overrides and CLI flag overrides on top. The `setup` command writes IDs and field mappings back to the config file using yaml.Node manipulation to preserve formatting.

## Snipe-IT Asset Mapping

- Device MAC → `serial` and `asset_tag`
- Device model → Snipe-IT Model (auto-created with manufacturer, category, fieldset)
- Custom fields mapped via `field_mapping` config (db column → UniFi attribute)
- Location set via `location_mapping` config (host name/ID → Snipe-IT location ID)
- Checkout uses adoption time as the checkout date
