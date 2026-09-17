# jev / TypeSafe — working notes

Verified against `jev-1.13.0` on 2026-09-17.

    POST https://api.typesafe.ai/v1/systemone
    Authorization: Bearer $API_KEY

Not a text generator: you give it `state` plus named typed questions, and it
returns structured answers. Question types: `noul` (0–1 veracity), `choice`
(pick one, with probabilities), `score` (rating, with probabilities).

Model string is `jev-latest`; the response reports the resolved version.

## Two things the docs get wrong or leave ambiguous

1. **`score.criteria` must be a JSON array, not a map.** Passing a map returns
   422 `list_type`. `choice.criteria` *does* take a map of option → description.
   The published schema shows `object | map | array` for both, which is wrong
   for `score`.

2. **`score` legends are 0-indexed.** Five labels come back keyed `0`–`4`, so a
   5-level scale is scored on 0–4, *not* 1–5. A returned `3.47` against
   Trivial/Minor/Moderate/Serious/Critical sits between **Serious** and
   **Critical** — read it as 1–5 and you understate severity by a full level.

Also: `noul` answers came back without the `confidence` field that `choice` and
`score` include, though the documented response schema lists it for all three.

## Latency

Questions really are evaluated in parallel — 1 question 0.47s, 4 questions
0.55s against the same state. Batch them rather than making N calls.
