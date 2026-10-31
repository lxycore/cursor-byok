# Maintaining cursor-byok

This repository is the community-maintained continuation of
[`leookun/cursor-byok`](https://github.com/leookun/cursor-byok), which was
archived in October 2026.

## Branch layout

- `main` tracks the current Rust/Tauri implementation. Compatibility fixes and
  releases are built from this branch.
- `legacy/cursor-ultra` preserves the older Go/Wails fork work that predates the
  upstream Rust rewrite. It is not merged into `main`.

## Compatibility baseline

The current baseline is Cursor `3.27.12`. Cursor updates can change both the
HTTP route surface and the protobuf payload dialect, so compatibility work must
be validated against real Cursor logs, not only unit tests.

Useful log locations:

- cursor-byok: `~/.cursor-byok-v3/logs/cursor-byok.YYYY-MM-DD.log`
- Cursor: `~/Library/Application Support/Cursor/logs/` on macOS

## Current Cursor workaround

Recent Cursor builds can enable the Statsig gate `agent_host_local_loop`.
That client-side loop emits `AgentRunRequest.conversation_state` as an ID
reference, while this server currently implements the server-side loop and
expects an inline `ConversationStateStructure`. Decoding the new payload fails
with protobuf wire-type errors.

Until the local-loop dialect is implemented natively, the Statsig bootstrap
response explicitly pins `agent_host_local_loop` off. This keeps agent runs on
the loop the server already implements. Do not remove the override without a
live Cursor regression test covering the new loop.

## Checks before merging

```bash
make check
```

This runs Rust formatting, Clippy with warnings denied, all workspace tests,
the frontend type checks, and the production frontend build. For changes to
Cursor replay or protocol handling, also run the relevant conversation in a live
Cursor build and inspect both log directories above.

## Release process

1. Update the version in `apps/desktop/package.json`,
   `apps/desktop/package-lock.json`, `apps/desktop/src-tauri/Cargo.toml`,
   `apps/desktop/src-tauri/tauri.conf.json`, and `Cargo.lock`.
2. Run `make check`.
3. Ensure the Tauri updater public key in `tauri.conf.json` matches the release
   signing key stored in the repository secret
   `TAURI_SIGNING_PRIVATE_KEY`.
4. Push the release commit to `main`, create an annotated `vX.Y.Z` tag, and push
   the tag. The release workflow builds Linux, Windows, macOS arm64, and macOS
   x64 artifacts, signs updater manifests, and publishes the draft release.
5. Install the produced artifact and smoke-test a new Cursor conversation before
   marking the release as latest.
