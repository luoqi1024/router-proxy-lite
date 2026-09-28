# Third-party components

- **sing-box v1.14.2** — https://github.com/SagerNet/sing-box/tree/v1.14.2
  Copyright (C) 2022 nekohasekai. GPL-3.0-or-later with the upstream naming/association notice preserved in its LICENSE. RouterLite is independent; no endorsement is implied. The registry modification is reproducible from `tools/core-profile.py`; no cryptographic implementations are authored here. Source archive SHA256: `67dd8f8c37ecaaadcfcafad1f0827eed4b034c963b86fd3aa5c0d7a36876845d`.
- **gopkg.in/yaml.v3 v3.0.1** — https://github.com/go-yaml/yaml/tree/v3.0.1 — MIT / Apache-2.0 terms in the upstream LICENSE, retained in module sources.
- **Go runtime / standard library** — https://go.dev/LICENSE — BSD-style license.
- **UPX 5.2.1** — https://github.com/upx/upx — executable compressor; its license includes the compressed-executable exception. Runtime binaries retain their own licenses.

The development bundle uses public CA and China rule-set files from the earlier local prototype. Their exact upstream revisions and redistribution notices must be pinned before public binary release. They are not tracked in this source repository.

Before distributing binaries: ship all required license texts, a complete corresponding-source archive for the modified core (including dependency versions and build instructions), and verified resource provenance. The current local preview is not represented as a distribution-ready release.
