# unifi2snipe

Sync network devices from the [UniFi Site Manager API](https://developer.ui.com/) into [Snipe-IT](https://snipeitapp.com/) asset management.

## Features

- Syncs all UniFi devices (APs, switches, consoles, etc.) into Snipe-IT as hardware assets
- Auto-creates Snipe-IT models for new device types
- Maps UniFi device attributes to Snipe-IT custom fields
- Checkout assets to Snipe-IT locations based on which UniFi controller manages them
- Supports dry-run mode for safe testing
- Configurable field mapping, location mapping, and product line filtering
- Rate limiting for large deployments

## Installation

**Download a pre-built binary** (recommended) from the [latest release](https://github.com/CampusTech/unifi2snipe/releases/latest):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/CampusTech/unifi2snipe/releases/latest/download/unifi2snipe-darwin-arm64 -o unifi2snipe
chmod +x unifi2snipe

# Linux (amd64)
curl -L https://github.com/CampusTech/unifi2snipe/releases/latest/download/unifi2snipe-linux-amd64 -o unifi2snipe
chmod +x unifi2snipe
```

Or install with Go:

```bash
go install github.com/CampusTech/unifi2snipe@latest
```

Or build from source:

```bash
git clone https://github.com/CampusTech/unifi2snipe.git
cd unifi2snipe
go build -o unifi2snipe .
```

## Quick Start

1. Copy the example config:

   ```bash
   cp settings.example.yaml settings.yaml
   ```

2. Fill in your API credentials in `settings.yaml`:
   - **UniFi API key**: Generate at [unifi.ui.com](https://unifi.ui.com) -> Settings -> API
   - **Snipe-IT API key**: Admin -> Personal API Keys
   - **`default_status_id`**: The ID of a deployable status label (e.g. "Ready to Deploy")

3. Run setup to create all required Snipe-IT resources:

   ```bash
   unifi2snipe setup
   ```

   This creates the Ubiquiti manufacturer, network/console categories, a UniFi Device fieldset, and all custom fields. The resulting IDs and field mappings are saved back to `settings.yaml`.

4. Run a dry-run sync:

   ```bash
   unifi2snipe sync --dry-run -v
   ```

5. Run for real:

   ```bash
   unifi2snipe sync -v
   ```

## Commands

### `setup`

Creates all required Snipe-IT resources (manufacturer, categories, fieldset, custom fields) and saves configuration back to `settings.yaml`.

```bash
unifi2snipe setup
```

### `sync`

Syncs UniFi devices into Snipe-IT.

```bash
unifi2snipe sync [flags]
```

| Flag | Description |
|------|-------------|
| `--dry-run` | Simulate without making changes |
| `--force` | Ignore existing values, always update |
| `--update-only` | Only update existing assets, never create new ones |
| `--use-cache` | Use cached data from `download` command |
| `--mac <addr>` | Sync a single device by MAC address |

### `download`

Downloads UniFi device data to a local cache for offline syncing.

```bash
unifi2snipe download
```

### Global Flags

| Flag | Description |
|------|-------------|
| `--config <path>` | Path to YAML config file (default `settings.yaml`) |
| `-v, --verbose` | Verbose output (INFO level) |
| `-d, --debug` | Debug output (DEBUG level) |
| `--log-file <path>` | Append log output to a file |
| `--log-format <fmt>` | Log format: `text` or `json` |

## Configuration

See [`settings.example.yaml`](settings.example.yaml) for all options. Key sections:

### UniFi

| Key | Description |
|-----|-------------|
| `api_key` | UniFi Site Manager API key |
| `base_url` | API base URL (default `https://api.ui.com`) |

### Snipe-IT

| Key | Description |
|-----|-------------|
| `url` | Snipe-IT instance URL |
| `api_key` | Snipe-IT API key |
| `manufacturer_id` | Ubiquiti manufacturer ID (auto-created by `setup`) |
| `default_status_id` | Status label ID for new assets |
| `category_id` | Fallback category ID for new models |
| `network_category_id` | Category for network devices |
| `console_category_id` | Category for console devices |
| `custom_fieldset_id` | Fieldset ID for custom fields (auto-created by `setup`) |

### Sync

| Key | Description |
|-----|-------------|
| `dry_run` | Simulate without making changes |
| `force` | Always update, ignore existing values |
| `rate_limit` | Enable Snipe-IT rate limiting |
| `update_only` | Never create new assets |
| `set_name` | Set asset name from UniFi device name on create |
| `checkout` | Checkout assets to locations via `location_mapping` |
| `product_lines` | Filter by product line (e.g. `network`, `protect`) |
| `host_ids` | Filter by specific UniFi controller IDs |
| `location_mapping` | Map UniFi host name/ID to Snipe-IT location ID |
| `field_mapping` | Map Snipe-IT custom field columns to UniFi attributes |

### Location Mapping

Map UniFi controllers to Snipe-IT locations. Devices managed by a matched controller are checked out to that location (when `checkout: true`), using the device's adoption time as the checkout date.

```yaml
sync:
  checkout: true
  location_mapping:
    "Office UDM": 1          # by host name
    "Warehouse UCK": 2       # by host name
    "abc12345-...": 3        # by host ID
```

### Environment Variables

| Variable | Overrides |
|----------|-----------|
| `UNIFI_API_KEY` | `unifi.api_key` |
| `UNIFI_BASE_URL` | `unifi.base_url` |
| `UNIFI_SNIPE_URL` | `snipe_it.url` |
| `UNIFI_SNIPE_API_KEY` | `snipe_it.api_key` |

## How It Works

1. Fetches all devices from the UniFi Site Manager API
2. For each device, looks up the corresponding Snipe-IT asset by MAC address (stored as serial number)
3. If the asset doesn't exist, creates it with the appropriate model, status, and custom fields
4. If the asset exists, compares field values and patches only what changed
5. Optionally checks out assets to Snipe-IT locations based on which UniFi controller manages them

### Custom Fields

The `setup` command creates these UniFi custom fields in Snipe-IT:

| Field | UniFi Attribute | Description |
|-------|----------------|-------------|
| UniFi: IP Address | `ip` | Device IP address |
| UniFi: MAC Address | `mac` | MAC address (colon-separated) |
| UniFi: Firmware Version | `version` | Current firmware |
| UniFi: Firmware Status | `firmware_status` | Update status |
| UniFi: Status | `status` | Connection status |
| UniFi: Product Line | `product_line` | Product line (network, protect, etc.) |
| UniFi: Model | `model` | Hardware model (e.g. U6-Pro) |
| UniFi: Is Managed | `is_managed` | Whether managed by a controller |
| UniFi: Is Console | `is_console` | Whether it's a console device |
| UniFi: Host Name | `host_name` | Controller name |
| UniFi: Host ID | `host_id` | Controller ID |
| UniFi: Adoption Time | `adoption_time` | When adopted by controller |
| UniFi: Note | `note` | Device notes |

## License

[MIT](LICENSE.md)
