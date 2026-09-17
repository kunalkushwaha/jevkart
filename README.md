# jevkart

A terminal car driven by an LLM, built to make **API latency visible**.

Frames tick every 60ms. [jev](https://docs.typesafe.ai) answers in ~200ms. It
physically cannot respond within a frame, so the car always reacts on a delay
you can watch: the obstacle approaches, the readout says `asking`, *then* the
car swerves. Whether it survives is a direct readout of round-trip time.

## How jev is used

There is no game AI here — no pathfinding, no rules engine. Every steering
decision is a network call.

Each time an obstacle comes within range, the game serializes the road into a
sentence and asks two questions in one request:

```
state: "A car drives left-to-right on a 3-lane road. The car is in lane 2.
        Ahead there is an obstacle in lane 2, 14 cells ahead; an obstacle in
        lane 1, 27 cells ahead."

move   (choice) up | hold | down   — which lane change keeps it safe?
danger (noul)                      — about to hit something if it does not move?
```

Both are evaluated in parallel, so the second question is nearly free
(1 question 0.47s → 4 questions 0.55s).

## Two modes — `tab` switches at runtime

| Mode | Who drives | What you learn |
|---|---|---|
| **auto** | jev | Pure latency demo. Crashes mean the round trip was too slow. |
| **manual** | you (`↑`/`↓`) | jev rides along as an advisor; the game scores how often you ended up where it said to go. |

In manual mode jev's suggested lane shows as a dim `░` ghost marker.

## Run

```sh
cp .env.example .env     # then add your TypeSafe key
go build -o bin/jevkart ./cmd/jevkart
./bin/jevkart                 # auto  — jev drives
./bin/jevkart -mode manual    # manual — you drive, jev advises
./bin/jevkart -bench 5        # headless: latency only, no TUI
```

`-bench` needs no terminal, which makes it the quickest way to confirm your key
works:

```
 1   559ms  move=down  conf=0.81 danger=0.67  jev-1.13.0
 2   198ms  move=down  conf=0.82 danger=0.72  jev-1.13.0
 5   172ms  move=down  conf=0.79 danger=0.68  jev-1.13.0

5/5 ok — avg 262ms  min 172ms  max 559ms
```

First call pays the TLS handshake; after that connection reuse brings it to
~190ms.

## Notes on the API

Two things worth knowing before you build against it — both cost me a failed
request to discover. See [NOTES.md](NOTES.md).

- `score.criteria` must be a JSON **array**, not a map (`choice.criteria` takes a map).
- `score` legends are **0-indexed**, so five labels are scored 0–4, not 1–5.

## Requirements

Go 1.27+, a terminal, and a TypeSafe API key. No third-party modules — stdlib
and ANSI escapes only.

## Configuration

The key is read from `API_KEY`, falling back to a local `.env`. `.env` is
gitignored and never committed.
