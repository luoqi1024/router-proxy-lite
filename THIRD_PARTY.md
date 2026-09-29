# Third-party components

RouterLite original code is GPL-3.0-or-later. Third-party code and data retain their respective terms. RouterLite is independent of SagerNet, Xiaomi, DB-IP and the other upstream projects; no endorsement is implied.

## Programs

- **sing-box 1.14.2** — [upstream source](https://github.com/SagerNet/sing-box/tree/v1.14.2), GPL-3.0-or-later. Copyright (C) 2022 nekohasekai. Its naming/association notice is preserved in `licenses/sing-box.txt`. RouterLite modifies protocol registration using `tools/core-profile.py`; it does not implement its own cryptography. Source archive SHA256: `67dd8f8c37ecaaadcfcafad1f0827eed4b034c963b86fd3aa5c0d7a36876845d`.
- **Go runtime and standard library** — BSD-style; `licenses/go-runtime.txt`.
- **Go modules linked into the manager and core** — exact identities and source checksums are recorded in the corresponding-source archive. `licenses/go-dependencies.txt` retains the discovered upstream LICENSE, COPYING and NOTICE texts from all 56 linked modules, including transitive dependencies. Regenerate it when the linked module set changes; see `tools/source-notices.py`.
- **gopkg.in/yaml.v3 3.0.1** — MIT / Apache-2.0 terms retained from upstream.
- **UPX 5.2.1** — [upstream](https://github.com/upx/upx); its license includes the compressed-executable exception. Runtime binaries retain their own licenses; the compression tool is not installed on the router.

## Data

- **Mozilla CA bundle, 2026-09-25**, converted by curl — [dated bundle](https://curl.se/ca/cacert-2026-09-25.pem), [source and extraction](https://curl.se/docs/caextract.html), MPL-2.0. Original file is used unchanged.
- **IP Geolocation by [DB-IP](https://db-ip.com)** — [IP to Country Lite](https://db-ip.com/db/download/ip-to-country-lite), [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). Input is the IPv4 CSV from the [sapics mirror at commit 3f18f6912c3898eddaf05e3ba50bc9d45b541040](https://github.com/sapics/ip-location-db/tree/3f18f6912c3898eddaf05e3ba50bc9d45b541040/dbip-country). RouterLite extracts CN ranges, combines adjacent CIDRs and converts them to sing-box SRS. These are modified data, not the full original database. DB-IP attribution and the license text are retained in `licenses/dbip.txt` and `licenses/cc-by-4.0.txt`; the management page links to DB-IP. This candidate replaces the earlier MaxMind-derived rule artifact.
- **v2fly/domain-list-community**, [commit bcea25493ed28c387660fe49ce1ceb242d2efca0](https://github.com/v2fly/domain-list-community/tree/bcea25493ed28c387660fe49ce1ceb242d2efca0), MIT. RouterLite resolves the `cn` list and filtered includes, deduplicates/sorts it and compiles SRS. The exact input archive is pinned; `licenses/domain-list-community.txt` preserves its notice.

Data conversion and notice collection were added on 2026-09-29. `rules.lock.json` pins original input sizes and SHA256 hashes; `assets.lock.json` pins runtime outputs. Conversion runs on a build computer and adds no Python dependency on the router. Data accuracy is not guaranteed; geographic classification alone does not guarantee a routing result.

## Corresponding source

`tools/source-bundle.py` collects the exact upstream core source, verified Go module ZIPs with their notices, tracked RouterLite code, build scripts and (with `--rule-inputs`) the pinned data inputs. `tools/verify-source.py` checks the extracted manifest and rebuilds using only its local module proxy and an already installed Go toolchain. The manager/core have been rebuilt offline with Go 1.27.1 and matched the original raw binaries byte for byte.

A public binary release must include its own matching corresponding-source archive, data inputs, notices and build instructions alongside the installation files. GitHub's automatically generated project source ZIP alone is insufficient. The first public binary is an experimental Alpha. Its release notes distinguish completed candidate checks from outstanding hardware and reboot validation; see `docs/RELEASE.md`.
