# Source POC attribution

The TDX archive binary format and provider URL were reviewed from Simon Lin's
[a-stock-data V3.10.2](https://github.com/simonlin1212/a-stock-data) and its references.
This repository implements its own bounded standard-library parser; it never downloads or executes SKILL.md.
The applicable Apache-2.0 license is retained in `LICENSE-a-stock-data.txt`.
Upstream document content hashes are recorded in `upstream-lock.json`.

The optional OpenBB provider is [OpenBB-finance/OpenBB](https://github.com/OpenBB-finance/OpenBB),
Apache-2.0. Reviewed package versions are `openbb-core==2.0.0`, `openbb-fred==2.0.0`.
OpenBB supplies the connector; the actual macro provider is the Federal Reserve Bank of St. Louis FRED.
Source-code licenses do not establish market-data redistribution rights.
