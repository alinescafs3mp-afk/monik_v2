# Gemma4 paired streaming source correction

`upstream/` contains the exact two Apache-2.0 image source files used as regression
fixtures. `../deployment/opt/spark/model-collection-v4/runtime/gemma4-source-overrides/`
contains the paired replacements. Their fixed sizes/SHA256 values are part of the
controller's Gemma-only file authority. `SOURCE-INVENTORY.json` records immutable
image source paths/hashes without host run IDs, receipts or credentials.

Run `python3 -B test_parser_offline.py`. The AST-based controls execute actual
parser and terminal-catchup methods with protocol-shaped records and no vLLM,
Torch, network or model imports. The original coalesced-call loss is reproduced;
patched split/coalesced/multi-tool, content, reasoning and terminal paths are
checked for exact-once names/IDs/JSON. This is source-level evidence only.

See [the integration notes](../MIDRANGE-SOURCE-2026-10-16.md) for the selected
loader chain, numerical-scrub and capacity limits, and separate live gates.
