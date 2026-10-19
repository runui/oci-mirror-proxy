# Experimental adaptive blob downloads

Enable `registries.<alias>.adaptive.enabled` to probe alternate upstreams when
whole-blob GET throughput falls below `min_bytes_per_second` over `window`.
Defaults are disabled, 262144 bytes/s, 10s, a 5s per-candidate `probe_timeout`,
and 3 `max_switches` probe rounds. Client Range requests are unchanged.

The original body remains open while probing. A replacement must return 206,
the exact delivered offset, the original total size and remaining length, and
no content encoding. Its first 512 KiB (or remaining bytes) must arrive at least
25% faster than the measured original rate, including connection/header time.
Probing runs in the background while the original stream continues forwarding.
The replacement sample is trimmed to the latest delivered offset before adoption.
If the original overtakes the entire sample, the alternate is discarded rather
than risking a gap. Remaining sampled bytes are replayed before the replacement body.
Rejected candidates are closed; if none qualifies, the original stream continues.
One untried candidate is probed per slow window, at most once per response. Slow sources are not globally
disabled. Docker still verifies the final digest.

## Limitations

- A window timer also runs during pending reads, allowing a completely stalled
  HTTP body to be replaced. Closing the abandoned HTTP body unblocks its read;
  a private, at-most-32-KiB buffer prevents writes into a returned caller buffer.
- Probes use at most 512 KiB of sample memory per active response, plus HTTP
  buffers. Each active upstream body has one persistent read goroutine and a
  reusable 32-KiB buffer; its timer is reused across reads. Abandoned HTTP bodies
  are closed and their read pumps stopped on switch or client cancellation.
- Finite samples do not guarantee sustained bandwidth after adoption.
- A slow downstream can look like a slow upstream. This is not bandwidth aggregation.
- An alternate may ignore Range or be slower, so enabling this can regress latency.
- `max_switches` bounds probe rounds, not successful switches or total candidates.

## Second iteration on 10.63

Asynchronous probes, stalled-read recovery and a larger 512-KiB sample were tested
with the same isolated setup. Controlled baseline runs took 32.080s / 32.107s;
adaptive took 3.012s / 3.008s, switching at offset 188416. All digests matched.
A source sending headers then no body was replaced in 3.019s, digest correct.
A fallback delaying 2s then returning 503 did not stop the original transfer:
the entire download finished in 32.091s, essentially the 32.080s baseline.

Three alternating public-source rounds on the 29785415-byte layer gave baseline
26.720s / 8.616s / 6.227s and adaptive 10.978s / 8.645s / 7.671s. All six digests
matched, but no switches occurred, so these differences cannot be attributed to
adaptive failover. The original production service remains unchanged. Raw results
are retained on the test host in `/DATA/oci-adaptive-test.UqdfEE/results-v2.json`
and `fault-results-v2.json`; experimental systemd units are stopped after testing.

## Third iteration and release decision

Persistent on-demand read pumps replace per-read goroutines/allocations. A local
1-MiB in-memory benchmark on Apple M4 Pro measured about 8.8 GB/s in adaptive
mode (34 allocations and roughly 35 KiB allocated per download), versus about
73 GB/s baseline. These are synthetic copy-path numbers, not network throughput;
adaptive mode still adds synchronization and copying overhead.

On 10.63 the controlled baseline took 32.088s; adaptive took 3.016s, stalled-body
recovery 3.020s, and failed-alternate continuation 32.171s. Eight concurrent
adaptive downloads completed in 3.105--4.134s, all digest-correct. Afterward the
controlled adaptive instance reported about 8.3 MB MemoryCurrent and 12 tasks.

Public-source baseline runs took 43.837s / 6.700s / 4.316s. Adaptive runs took
38.816s / 52.474s / timeout at 60s (19277976 of 29785415 bytes). Completed bodies
passed their digests; the partial response is not a successful integrity check.
Logs showed alternates rejected because their samples were not sufficiently
faster. No successful public-source switch was observed. Raw results are retained
at `/DATA/oci-adaptive-test.UqdfEE/results-v3.json`.

Keep the feature opt-in. Controlled tests establish protocol continuity and
recovery, not improved performance across arbitrary public mirrors. Production
Docker configuration and the production proxy binary were not changed. There is
no new on-disk format or state: rollback is disabling `adaptive.enabled` and
reloading, with existing requests retaining their original strategy.

## Experiment on 10.63 (2026-10-19)

Independent loopback-only proxy instances were used; the production proxy and
Docker configuration were not replaced. Controlled sources served the same 2 MiB
body: the first at 64 KiB/s and the alternate without throttling. With a 3s window,
baseline runs took 32.074s / 32.074s; adaptive took 3.015s / 3.018s. Both switches
occurred at byte 196608; all four complete bodies passed SHA256 checks.

Real outlinewiki/outline blobs were also tested with identical upstream order
(1ms, 1panel, DaoCloud). A 7155934-byte blob took baseline 3.597s / 3.127s and
adaptive 4.486s / 1.838s, all digest-correct. A 29785415-byte blob took baseline
5.596s / 6.160s; adaptive timed out after 75s with only 6920022 bytes, then completed
in 38.490s with a correct digest. There were no successful adaptive-switch logs
in those real-source runs; an initial 1ms header timeout was observed. Public
upstream variability and routing/fallback confound this comparison. These results
prove controlled failover feasibility, not a public-network speed improvement.
