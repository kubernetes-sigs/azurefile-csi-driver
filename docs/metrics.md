# Filesystem metrics

The Linux Azure File CSI node driver exports filesystem statistics from the
kernel for locally mounted Azure File CSI volumes.

## Metric semantics

These metrics are **gauges**, even though their underlying kernel values are
cumulative counters:

| Metric | Value |
|--------|-------|
| `azurefile_csi_driver_volume_read_bytes` | Sum of bytes-read counters from currently present sources |
| `azurefile_csi_driver_volume_written_bytes` | Sum of bytes-written counters from currently present sources |
| `azurefile_csi_driver_volume_operation_requests` | Sum of operation-request counters from currently present sources |
| `azurefile_csi_driver_volume_operation_errors` | Sum of failed-operation counters from currently present sources |

Values are rebuilt on each scrape and grouped by `protocol`, `storage_account`,
and `file_share`. Operation metrics also have an `operation` label. Each node
reports its own totals; preserve your scrape target labels when inspecting
individual nodes.

These are not lifetime totals or counts of activity since the previous scrape.
Adding a source can add its existing counters, and unmounting a filesystem,
removing an SMB tree connection, or resetting kernel counters can reduce a value
without new I/O. When no matching sources remain, the filesystem series is
omitted rather than retained at zero.

For example, two independent connections reporting 100 and 200 bytes produce a
gauge value of 300. Removing the first connection leaves 200, even if no I/O
occurred between scrapes.

Exporting this sum as a counter would not make that decrease a valid reset of
the aggregate: the remaining connection has not reset. Prometheus counter-reset
handling could interpret the remaining 200 bytes as new activity. Gauges avoid
declaring this aggregate to be a monotonic counter.

## Querying the gauges

Use the raw gauge to inspect the current sum of kernel totals:

```promql
azurefile_csi_driver_volume_read_bytes{protocol="smb", storage_account="account", file_share="share"}
```

To inspect the net change in that gauge over five minutes:

```promql
delta(azurefile_csi_driver_volume_read_bytes{protocol="smb"}[5m])
```

To inspect its estimated per-second trend over five minutes:

```promql
deriv(azurefile_csi_driver_volume_read_bytes{protocol="smb"}[5m])
```

`delta()` and `deriv()` describe changes in the aggregate gauge, including source
additions, removals, and resets. Negative results are expected. **Neither query
reliably measures I/O volume or throughput across source lifecycle changes.**
Clamping negative results to zero does not recover lost activity or distinguish
source changes from I/O.

Do not use `rate()` or `increase()` on these gauges. Accurate throughput requires
stable per-source counters with reset handling before aggregation, or a collector
that accumulates reset-aware per-source deltas; these aggregate gauges do not
provide that information.

## Migrating dashboards

Replace the former counter names with the gauge names:

| Former name | Current name |
|-------------|--------------|
| `azurefile_csi_driver_volume_read_bytes_total` | `azurefile_csi_driver_volume_read_bytes` |
| `azurefile_csi_driver_volume_written_bytes_total` | `azurefile_csi_driver_volume_written_bytes` |
| `azurefile_csi_driver_volume_operation_requests_total` | `azurefile_csi_driver_volume_operation_requests` |
| `azurefile_csi_driver_volume_operation_errors_total` | `azurefile_csi_driver_volume_operation_errors` |

Update recording rules, alerts, and dashboard units as well as metric names.
Remove counter-based throughput and operation-rate interpretations. Replacing
`rate()` with `deriv()`, or `increase()` with `delta()`, is appropriate only when
the panel is explicitly intended to show the aggregate gauge's trend or net
change, not actual I/O activity.
