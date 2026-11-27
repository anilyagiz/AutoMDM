# charts-collect

Fetches live data from an AutoMDM instance via the REST API and writes chart rows
into a local database. Designed to run hourly via cron.

## What it collects

- **Uptime** — fetches currently online hosts and ORs them into the current
  hour's `host_scd_data` row (`dataset='uptime'`).
- **CVE** — fetches per-host vulnerabilities, inverts into per-CVE host
  bitmaps, and reconciles into `host_scd_data` (`dataset='cve'`). Unchanged
  CVEs keep their open row; changed bitmaps close the prior-day row and open
  a new one for today; intra-day changes overwrite today's row via ODKU.

## Usage

```bash
go run ./tools/charts-collect \
  --automdm-url https://dogfood.fleetdm.com \
  --automdm-token <token>

go run ./tools/charts-collect \
  --automdm-url https://dogfood.fleetdm.com \
  --automdm-token <token> \
  --mysql-dsn "automdm:automdm@tcp(localhost:3306)/automdm"
```

## Flags and env vars

| Flag | Env | Description |
|------|-----|-------------|
| `--automdm-url` | `AUTOMDM_URL` | AutoMDM server URL (required) |
| `--automdm-token` | `AUTOMDM_TOKEN` | AutoMDM API token (required) |
| `--mysql-dsn` | `MYSQL_DSN` | Full MySQL DSN |

If `--mysql-dsn` / `MYSQL_DSN` is not set, the DSN is assembled from the same
env vars used by the automdm server (so the same values can be reused, e.g. via
Render `fromService`):

- `AUTOMDM_MYSQL_ADDRESS`
- `AUTOMDM_MYSQL_USERNAME`
- `AUTOMDM_MYSQL_PASSWORD`
- `AUTOMDM_MYSQL_DATABASE`

## Notes

- SCD encoding constants (`9999-12-31` open sentinel, batch size) mirror
  `server/chart/internal/mysql/data.go`. Keep in sync when either side changes.
- Errors in one collector (uptime/cve) are logged but do not block the other.
