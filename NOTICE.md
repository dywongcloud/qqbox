# Tencent qqbox

This repository is **Tencent qqbox**, a rebrand of the upstream laboon
sandbox runtime. The implementation is Rust (control plane, CLI, daemon) and
Zig (freestanding guest stubs). All Go source and Go build tooling were
removed; see `docs/PORT_STATUS.md` for the honest runtime boundary.

Traditional Chinese documentation: `zh-hant/README.zh-Hant.md`.

## Upstream attribution

The upstream project is laboon, Copyright 2026 Bunshin Labs LLC, licensed
under Apache License 2.0 (see `LICENSE` and `COPYRIGHT`). That attribution is
preserved. Two upstream identifiers remain in binary/text artifacts:

| artifact | identifier | why |
| --- | --- | --- |
| `stub/*.bin`, `stub/macos/*.bin`, `stub/windows/*.bin` | `.note.laboon` | ELF note section name frozen in the committed Zig 0.13.0 blobs; rebuilding them needs Zig 0.13.0, and the Rust workspace embeds the blobs byte-identical via `include_bytes!`. |

Also note the upstream whale wordmark in `docs/assets/logo.svg` is a vector graphic; its path IDs and text metadata were rebranded to qqbox, but the drawn letterforms still spell the upstream word. This is a graphic asset, not an identifier.

## No endorsement implied

"Tencent" is the requested brand label. Nothing in this repository claims
endorsement, sponsorship, or ownership by Tencent Holdings Limited beyond the
branding applied to this fork.
