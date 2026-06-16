# Feed Adapters

## Feed format

Canonical custom feed format:

```json
{
  "entries": [
    {
      "category": "ai",
      "service": "openai",
      "cidrs": ["104.18.0.0/16", "172.64.0.0/13"]
    }
  ]
}
```

A single entry object and a top-level entry array are also accepted. Prefixes
are normalized and deduplicated. Selecting a category also includes services
added to it in future feed updates.

## Feed data parameterization

Each feed can carry an optional `data` JSON string for adapter parameterization.
This allows one adapter to serve multiple feeds with different settings. For
example, an SRS adapter can be reused for different geoip files by setting the
`category` and `service` in the feed data:

```json
{"category": "Russia", "service": "geoip-ru"}
```

The `data` field is available inside the adapter as `feed.data` (a raw JSON
string — use `JSON.parse()`).

## Built-in adapters

The distribution includes adapters for canonical JSON, OpenCCK, IPRanges, and
sing-box SRS. A feed selects one adapter.

- **Canonical JSON** — processes the canonical feed format described above.
- **OpenCCK** — processes the OpenCCK feed format for broad service coverage
  based largely on ASN and shared infrastructure ranges.
- **IPRanges** — downloads merged IPv4/IPv6 lists from
  [antonme/ipranges](https://github.com/antonme/ipranges) and maps them into
  separate catalog services.
- **sing-box SRS** — downloads `.srs` binary files, decompresses (zlib), parses
  the binary format (versions 1–5), and extracts `ip_cidr` items. Use
  `api.srsGet(url)` for direct access in custom adapters.

## Adapter API

Feed adapters are JavaScript programs stored in the database and editable from
the admin UI. An adapter must define:

```javascript
function sync(feed, api) {
    const data = JSON.parse(api.httpGet(feed.url));
    return data.entries;
}
```

### sync(feed, api)

The function returns objects with `category`, `service`, and `cidrs` fields.

### api.httpGet(url)

Makes an HTTP GET request and returns the response body as a string.

### api.srsGet(url, cfg)

Downloads and parses sing-box SRS binary files. `cfg` is an optional JSON string
(e.g. `{"cidrs":true}`) that controls which data to extract. Currently only
`cidrs` is supported.

### api.log(msg)

Logs a message to the application log.

## SRS binary format

The sing-box SRS format stores compiled IP CIDR ranges in a zlib-compressed
binary file. Versions 1 through 5 are supported. The adapter extracts
`ip_cidr` items for both IPv4 and IPv6 prefixes.

## Limits and security

Requests are limited by host, count, response size, total downloaded size, and
execution timeout. The feed host is always allowed; adapters may declare
additional hosts in the admin UI.

The Go application validates and normalizes every returned CIDR before atomically
replacing the previous snapshot. Invalid scripts and failed synchronizations
leave the last successful snapshot in place.

## Adapter management

### Editor

Each adapter has a separate editor page. Syntax and runtime failures are shown
there with JavaScript source locations and stack traces while preserving the
submitted source. Built-in adapters can also be reset to the distribution
version; reset restores their original name, source, and allowed hosts and
increments the revision.

### Testing

An existing adapter can be tested against one of its feeds from the admin UI.
The test uses the source currently shown in the editor, including unsaved
changes, and previews up to 100 normalized CIDRs without modifying the catalog,
feed status, adapter revision, or BGP state.

### Backups

When an adapter's source is updated or the adapter is deleted, the previous
source is backed up to `WDBGP_ADAPTER_BACKUP_DIR` (default
`<db_dir>/backup/adapters`). The directory is created automatically. Set
`WDBGP_ADAPTER_BACKUP_DIR` to an empty string to disable backups.
`WDBGP_ADAPTER_BACKUP_MAX` (default 10) controls how many backup copies are
kept per adapter; oldest files are deleted first. 0 means unlimited retention.
