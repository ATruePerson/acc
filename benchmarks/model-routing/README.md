# ACC model-routing benchmark

This suite evaluates configured model IDs through ACC's real `/v1/responses`
surface. It records provider failures separately from model, tool-formatting, and
answer failures. Tool workflows run against an in-memory fixture repository;
model-generated writes and commands never touch the real checkout.

The normalized reasoning policy is `reasoning.mode: maximum`. Each hidden
benchmark model maps that policy to its own provider-supported request fields in
`config.json`; the runner requests the model's exact `max` effort and records the
actual `X-ACC-*` response headers.

Run the full matrix:

```bash
go run ./benchmarks/model-routing/runner -profile full
```

Useful narrower profiles:

```bash
go run ./benchmarks/model-routing/runner -profile probe
go run ./benchmarks/model-routing/runner -profile core -models bench-opencode-big-pickle,bench-nvidia-nemotron-super
```

Raw runs are written to `raw-results/`. `results.json` and `report.md` are
regenerated from the current invocation. Provider limits may justify fewer than
five important-tool runs; every skipped or failed run remains explicit.
