# MemoryAgentBench — Conflict Resolution track

Dataset: [ai-hyz/MemoryAgentBench](https://huggingface.co/datasets/ai-hyz/MemoryAgentBench)
(MIT). Ghost runs the Conflict Resolution split ("factconsolidation") as an
LLM-free retrieval benchmark: facts are ingested in stream order (the store
clock advances one minute per fact), later facts update earlier ones, and a
question scores as a hit when a gold answer string appears in a top-k
retrieved memory.

## Setup

```bash
# Download the parquet split
curl -sL "https://huggingface.co/datasets/ai-hyz/MemoryAgentBench/resolve/main/data/Conflict_Resolution-00000-of-00001.parquet" -o cr.parquet

# Convert to the harness JSON (needs pyarrow: pip install pyarrow)
python3 - <<'PY'
import pyarrow.parquet as pq, json
t = pq.read_table('cr.parquet').to_pylist()
out = [{
    "id": r["metadata"]["qa_pair_ids"][0].rsplit("_no", 1)[0],
    "context": r["context"],
    "questions": r["questions"],
    "answers": r["answers"],
} for r in t]
json.dump(out, open('conflict_resolution.json', 'w'))
PY

# Run (6k/32k/64k tracks; 262k skipped unless GHOST_BENCH_MAB_MAX_FACTS is raised)
GHOST_BENCH_MAB=testdata/memoryagentbench/conflict_resolution.json \
  go test ./internal/store/ -run TestMemoryAgentBenchCR -v -timeout 30m
```

## Baseline (2026-07-12, FTS-only default path)

| track | hit@5 | hit@10 | MRR |
|---|---|---|---|
| sh_6k  | 0.590 | 0.660 | 0.447 |
| sh_32k | 0.320 | 0.430 | 0.213 |
| sh_64k | 0.340 | 0.440 | 0.234 |
| mh_6k  | 0.150 | 0.260 | 0.101 |
| mh_32k | 0.070 | 0.120 | 0.045 |
| mh_64k | 0.110 | 0.180 | 0.059 |

Multi-hop (mh) chains two facts with no shared vocabulary — the known
FTS-only ceiling; embeddings and entity-bridge retrieval are the levers.
