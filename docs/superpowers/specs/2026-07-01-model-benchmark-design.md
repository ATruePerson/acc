# Model Benchmark Tool — Design Spec

Date: 2026-07-01

## Motivation

The last five commits to `config.json` hand-tweaked temperature/top_p back and
forth (2.0 → 0.9 → 1.2 → 0/0 → 0.6/0.95) chasing "creative coherence" and
fixing "high-temp gibberish" — guesswork with no objective signal. Separately,
there's a standing belief about relative model quality (Fable/Mythos's backing
model considered top-class across coding/reasoning/creativity/math, Opus
second, Sonnet solid for regular tasks, Haiku fast-but-shallow) that has never
been tested against the actual configured backends.

This tool replaces both of those with one repeatable command: `acc bench`.
It answers "which backend wins this category" empirically, and "did my last
config change help" by diffing against history.

## Scope

Full cross-matrix: every tested backend runs every prompt category, not just
the category its persona is currently assigned to. This is deliberate — it's
the only way to discover that, say, Fable/Mythos's backing model out-codes
Opus's, which a task-matched-only design could never surface.

Out of scope for v1 (explicitly deferred, not forgotten):
- Multi-sample averaging per prompt (run each prompt once; revisit if scores
  prove noisy in practice)
- More than 2 prompts per category
- A `--compare <run_id>` flag for arbitrary historical diffs (v1 only diffs
  against the immediately preceding run)
- Web dashboard / TUI integration (terminal + files only for v1)

## Test matrix — 7 configs

| Identity | Category label | Variant | Model | Provider |
|---|---|---|---|---|
| Opus | coding | primary | nemotron-3-ultra-550b-a55b | nvidia |
| Opus | coding | fallback | deepseek-v4-pro | nvidia |
| Sonnet | creative | primary | nemotron-3-super-120b-a12b | nvidia |
| Sonnet | creative | fallback | big-pickle (deepseek-v4-flash) | opencode |
| Haiku | quick | (no fallback configured) | gemini-3.1-flash-lite | gemini |
| Fable/Mythos | fiction | primary | gemini-3.1-pro-preview | gemini |
| Fable/Mythos | fiction | fallback | minimax-m3 | nvidia |

`fable` and `mythos` aliases are byte-identical in `config.json` today (same
model, system prompt, fallback) — tested once as "fable/mythos". If the two
configs ever diverge, split back into separate rows.

Each config gets every prompt from every category (4 categories × 2 prompts =
8 prompts), so total jobs = 7 configs × 8 prompts = **56 generation jobs**,
each followed by one judge call = **56 judge calls**, **112 calls per run**.

## Prompts

**coding**
1. `coding-1`: "Write a Go function `parseDuration(s string) (int, error)` that
   parses strings like '1h30m', '45m', '2h' into total seconds. Handle invalid
   input with a clear error. No external libraries."
2. `coding-2`: "Find and fix the bug in this Go function, explaining the
   mistake in one sentence:
   ```go
   func lastN(items []int, n int) []int {
       if n > len(items) {
           n = len(items)
       }
       return items[len(items)-n : len(items)-1]
   }
   ```"
   (Bug: the slice end bound should be `len(items)`, not `len(items)-1` —
   the function drops the actual last element.)

**creative**
1. `creative-1`: "Write the opening paragraph of a story: a soldier returns to
   a village that no longer remembers the war he fought in."
2. `creative-2`: "Write a tense dialogue exchange between two characters who
   both want the same thing but can't say so directly."

**quick**
1. `quick-1`: "Summarize this in 2 sentences: 'The city council voted 6-3
   Tuesday night to approve a new transit line connecting the eastern
   suburbs to downtown, with construction expected to begin in early 2027
   and finish by 2030. The $340 million project will add four new stations
   and is funded through a mix of state grants and a local sales tax
   increase approved by voters last year. Supporters say it will cut commute
   times by up to 25 minutes for an estimated 40,000 daily riders, while
   opponents have raised concerns about construction disruption to small
   businesses along the route. The council also approved a separate measure
   to expand bus service in the interim.'"
2. `quick-2`: "If a train leaves at 3:15pm going 60mph and another leaves the
   same station at 3:45pm going 90mph in the same direction, when does the
   second train catch the first?"

**fiction**
1. `fiction-1`: "Continue this scene in the same voice: 'The Ranger paused at
   the treeline, where the bark had gone the color of old bruises. No birds
   called here, and the silence had a texture, like held breath.'"
2. `fiction-2`: "Describe, in-world, what wakes in the dark places between the
   roots of the world tree when it has not fed in a hundred years."

Exact prompt text above is final and lives as a Go literal in `bench.go`, not
regenerated per run — the whole point is a fixed, comparable prompt set
across time.

## Judging

Each generation response is graded by one call to **glm-5.1 on NVIDIA**
(free, not a contestant in any category — avoids a model grading itself or a
sibling). Judge call template:

```
You are grading an AI model's response for quality.
Task category: {category}
Original prompt: {prompt}
Response to grade: {response}
{category_rubric}
Respond with ONLY a JSON object: {"score": <integer 1-10>, "rationale": "<1-2 sentence explanation>"}
```

Per-category rubric line:
- coding: "Score on correctness (does the logic work), idiomatic Go style,
  edge-case handling. Code that wouldn't compile or is wrong scores 1-3."
- creative: "Score on voice/tone, prose craft, originality. Grammatically fine
  but flat or generic prose scores 4-6."
- quick: "Score on factual/logical accuracy and conciseness. A wordy but
  correct answer scores lower than a tight correct one."
- fiction: "Score on consistency with a dark-fantasy register, immersion, and
  avoiding flat/translated-sounding phrasing."

If the judge reply isn't parseable JSON, retry once; if it still fails,
record that job's score as `null` with `error: "judge_parse_failed"` and move
on. One bad job must never abort the run.

## Architecture

New `bench.go` at repo root (flat layout, matches existing convention). New
`bench` subcommand wired into the existing dispatch in `main.go` alongside
`doctor`/`models`/`claude`.

Runs **in-process** — does not require the proxy daemon to be running:
1. Load config via the existing `loadConfig`.
2. Build the 7 `Route` values directly from the parsed routes/aliases/
   fallbacks (reusing existing `Route`/`Config` types from `types.go`).
3. For each of the 56 (config × prompt) pairs, build the outgoing request
   with the existing `translateRequest(ar, route, cfg)`, POST it directly to
   that provider's `base_url`.
4. Immediately follow with the judge call (same machinery, judge's Route).
5. Append one JSONL line per completed job as it finishes (not batched at
   the end — a mid-run crash shouldn't lose already-finished work).

A single failed job (timeout, provider error, judge parse failure) is logged
with an `error` field and the run continues; it never aborts the other 55.

## Concurrency

Worker pool capped at 5 concurrent jobs (each job = generate, then judge,
done back to back within that job). Bound chosen to keep NVIDIA NIM's rate
limit comfortable while still cutting wall-clock from ~15-25 min sequential
to roughly 4-6 min. Opus's primary (nemotron-ultra-550b) stays the long pole
per job but no longer gates the whole run.

## Output

**`bench_runs.jsonl`** (repo root, same append-only pattern as the existing
`test_runs.jsonl`, gitignored as a runtime artifact) — one line per completed
job:

```json
{"run_id":"20260701-143200","timestamp":"2026-07-01T14:34:51+05:30","identity":"opus","variant":"primary","model":"nemotron-3-ultra-550b-a55b","provider":"nvidia","category":"coding","prompt_id":"coding-1","score":8,"rationale":"Correct, handles invalid input, idiomatic.","latency_ms":14230,"tokens_in":142,"tokens_out":387,"error":null}
```

`run_id` is a compact `YYYYMMDD-HHMMSS` (local time, no colons) generated once
per `acc bench` invocation — used both as the JSONL field and as the markdown
report's filename, so it must stay filesystem-safe. `timestamp` is the
full-precision completion time of that specific job and can use normal
RFC3339 formatting since it's never used as a filename. The variant field is
named `variant` (not `config`) to avoid colliding with "config" meaning
`config.json` elsewhere in this doc.

Full prompt/response text is deliberately **not** stored in the JSONL (keeps
it lightweight, matches the metrics-only style of `test_runs.jsonl`) — that
detail goes in the markdown report instead.

**Terminal**, live during the run:
```
[12/56] sonnet/fallback · creative-2 ... 7.2s, score 8/10
```
then a summary table (identity × category, avg score + avg latency) at the
end, then a diff against the immediately preceding `run_id` in the JSONL for
matching (identity, variant, category) cells — e.g. `sonnet/primary creative:
7.4 → 8.1 (+0.7)` — so a config tweak's effect is visible without manual
comparison.

**Markdown report**, `bench_runs/<run_id>.md` — full detail per job: prompt
text, full response, score, judge rationale. This is where you read the *why*
behind a number.

## CLI usage

```
acc bench
```

Runs the full 112-call matrix, prints progress + summary + diff, writes
`bench_runs.jsonl` (append) and `bench_runs/<run_id>.md` (new file). No flags
needed for v1.

## Testing

Standard Go table-driven tests for the pure logic: JSONL line construction,
judge-JSON parsing (including the malformed-reply retry/fallback path), and
the diff-against-previous-run calculation. The actual provider calls are not
unit tested (no good way to mock 7 live upstreams cheaply) — `acc bench`
itself, run by hand against the real providers, is the integration test.
