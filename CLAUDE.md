# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Bounce is a distributed, metadata-protecting group chat app. Every instance runs its own Tor hidden service; all device-to-device traffic goes over Tor. There are no servers — devices gossip signed frames to each other directly.

`docs/design.md` is the authoritative protocol description (device groups, scopes, the reference flow, group consensus, files, encrypted devices). Read it before touching anything in `chat/`. `docs/goals.md` records the non-negotiable constraints (no data on hardware the user doesn't control, no central nodes, no global user namespace).

## Repository layout

The repo has one git submodule — clone with `--recurse-submodules`. `go-arti/` is the Tor implementation (see Build); `go.mod` has `replace github.com/bounce-chat/go-arti => ./go-arti`.

Fyne is now tracked upstream (`fyne.io/fyne/v2 v2.8.0`, no `replace`). It used to be a fork whose reason for existing was camera capture; that now lives in `ui/camera_supported.go` / `ui/camera_unsupported.go` over `github.com/svanichkin/gocam`, split by `//go:build linux || windows`. The unsupported stubs `log.Fatal`, so every call site guards with `cameraSupported()` — keep that up if you add one.

## Build

Arti is Rust and the Go toolchain cannot invoke Cargo, so the static library has to exist before the first `go build`:

```bash
cd go-arti && make lib            # host target; needs a Rust toolchain, takes a couple of minutes
make lib GOOS=windows             # additionally, to cross-compile Windows
make android                      # additionally, for the Android ABIs
```

The result lands in `go-arti/lib/<goos>_<goarch>/libarti_ffi.a`, which is where the `#cgo LDFLAGS` in `go-arti/internal/arti/cgo.go` look. Always rebuild through `make lib` rather than calling `cargo` directly: Go's build cache doesn't hash a library reached via `LDFLAGS`, so `make lib` also regenerates `internal/arti/libstamp.go` to give the cache something Go-visible to notice. Without it, editing the Rust and rebuilding silently relinks nothing.

Then, from the repo root:

```bash
go build                          # desktop dev build (linux/macOS)
make windows                      # cross-compile via mingw32
make android                      # Kotlin client -> Bounce.apk (gomobile bind + gradle)
make android-bind                 # regenerate the .aar only
```

Release targets (`macos-release`, `windows-release`, `android-release`, `linux-arch-release`, `linux-debian-release`) sign/notarize/package into `releases/` and read credentials from `.env`. The desktop and Windows release builds pass `-tags migrated_fynedo`; plain `go build` does not.

`make android` needs the Android SDK, an NDK, and the Arti libraries for each ABI it binds (`NATIVE_ABIS`, default `android/arm64,android/amd64`) — `cd go-arti && make android` builds all of them. `ANDROID_SDK` defaults to `~/Android/sdk`; `ANDROID_NDK` is not pinned but discovered as the highest version installed under `$(ANDROID_SDK)/ndk`, so it does not rot when the NDK is upgraded. Override either on the command line or in `.env`. There is no dex2jar step and no forked-CLI prerequisite — that pipeline belonged to the Fyne Android client, which was replaced.

`go-arti`'s own `make doctor` reports which cross-compilation toolchain is missing for a target, and is a faster way to diagnose a failed release build than reading a linker error.

## Test

Only `chat/` has tests, and they are fast (<1s) because they run against an in-process fake network.

```bash
go test ./chat/                              # whole suite
go test ./chat/ -run TestEarlyConfirmationWorks -count=1
go vet ./chat ./ui ./network ./config
```

`chat/fixture_test.go` is the harness: `testnet` implements `chat.Network` over `net.Pipe` with real ed25519 onion-style keys, `testUI` implements `chat.UI` and records calls. Tests spin up multiple full `*Bounce` instances (`newBounceUser("Alice")`), wire them together, and synchronize on events rather than sleeping — use `await(t, b, "UIMethodName")` and `awaitAck(t, to, from, frameType, frameID)`, both of which fail the test after 2s. `createUsersAndGroups(t)` builds three users in a shared group and registers cleanup.

## Architecture

Three packages with strictly one-way dependencies:

- **`chat/`** — the engine. Owns the database, the protocol, peering, crypto. Nearly everything is unexported.
- **`ui/`** — Fyne desktop UI. Talks to the engine only through the interfaces below.
- **`network/`** — Tor transport, a single file (`network/tor.go`) over `go-arti`. (`bine` is still in `go.mod`, but only `chat/fixture_test.go` uses it, for onion-style ed25519 keys.)

`android/goengine` is a fourth Go package but not a fourth layer: it is a gomobile binding surface over the same `chat.Engine`, so the Kotlin client is a second consumer of the seam `ui/` uses rather than something built on top of it.

The seam between engine and UI is two interfaces in `chat/`:

- `chat.Engine` (`chat/engine.go`) — every action the UI can take.
- `chat.UI` (`chat/ui.go`) — every event the engine pushes out, plus the plain DTO structs (`User`, `Group`, `DirectMessage`, `InitialState`, …) that cross the boundary.

The UI never sees a frame, a `gorm.DB`, or a network address. Adding a user-facing feature means extending both interfaces and their implementations. `chat.Network` (`chat/network.go`) is the third seam — any overlay network satisfying it can host Bounce, which is what makes the test harness and a future Tor replacement possible.

Device identity *is* the network key. The engine trusts `conn.RemoteAddr()` as an authenticated peer identity everywhere, which is only sound because the transport authenticates it first: both `Accept` and `Dial` in `network/tor.go` run a mutual challenge/XOR/signature handshake before returning a conn, so an implementation of `chat.Network` that skips this breaks every authorization check downstream.

`main.go` picks a mode: `ui.Main()` normally, or `chat.StartEncryptedDevice(...)` with `-encrypted` (a headless store-and-forward node that relays ciphertext it cannot read).

### Tor transport

`network/tor.go` runs Arti — the Tor Project's Rust implementation — statically linked through cgo. It is not C Tor: there is no control port, no circuit-level control, and no torrc (the migration deleted the one Bounce used to write). Configuration is the `arti.Config` struct.

A `session` is one generation of `{client, onion}` and is never mutated. `Restart` builds a whole new session and swaps it in, so callers see a matched pair or none — never a client from one generation beside an onion from the next. `awaitSession()` is the only way to reach either, and it *blocks* during a restart rather than failing fast, because the accept loop retries non-fatal errors immediately with no backoff and would otherwise spin a core.

Two Arti properties leak into the design and are worth knowing before changing either side:

- **Onion keys are ephemeral.** Arti's keystore is in-memory, so Bounce persists its own key in `tor/keys/private_key` and passes it back to `Listen` on every start. The 64-byte expanded ed25519 format is what C Tor controllers used and the path did not change in the migration, so installs from the go-libtor era keep their `.onion` address.
- **`Ready` never retracts.** Arti derives it from timestamps written on success and never cleared, so once bootstrapped it stays true for the life of the process even with the network unplugged; there is no `NETWORK_LIVENESS` equivalent. `watchOnlineStatus` maps `status.Ready` onto the `NetworkOnline`/`NetworkOffline` callbacks, which means **the transport cannot report that connectivity dropped**.

That last point is why `chat.Network` has a `Restart() error` method and why `monitorNetworkAndRestartWhenNeeded` (`chat/device_pool.go`) exists: every 3 minutes, if this process has ever accepted or dialled a connection and the socket count has been zero across two consecutive samples, the engine declares the network dead, calls `Restart()`, then clears the dial cooldowns and the whole device pool and re-peers. Detection lives in the engine because only the engine can see that traffic stopped. Any new `chat.Network` implementation has to provide `Restart()`.

### Frames

All communication is asynchronous frames — no request/response. Wire format is TLV: 2-byte type, 4-byte length, msgpack payload (`chat/wire.go`). Frame type constants and the two handler maps (normal device / encrypted device) live in `chat/protocol.go`. A received frame is dispatched to `handleX(peer, payload, catchUp)`; if the handler returns a non-nil `broadcastable`, `readFrames` acks it and gossips it onward automatically (`chat/remote_device.go`).

Most frames are wrapped in a `signedContainer` (blake3 hash of the payload, signed by the device's network key). Frame structs embed `SignedFrame` and `cachedEncoding`; fields tagged `msgpack:"-"` are local-only and never cross the wire, which matters because the signature covers `OriginalPayload` verbatim — re-marshalling a frame and re-signing it produces a different frame.

Handlers must verify: valid signature, signer device not revoked *at the time the frame was written*, and signer belongs to the claimed author (`b.signedByUser`). See `chat/direct_message.go` for the canonical shape.

### Scopes and delivery

`b.broadcast(br)` resolves the frame's scope to a set of device addresses (`chat/protocol.go`: `getSyncScope`, `getUserScope`, `getGroupScope`, `getGroupWithInvitesScope`, `getGlobalScope`, `getCustomScope`), skipping devices already known to have it. Delivery is tracked by `deliveryRecord` rows written when an ack arrives; there is no indirect knowledge of who holds what.

When two devices connect they run the reference flow — offer (`chat/reference_offer.go`) → request (`chat/reference_request.go`) → catch up (`chat/catch_up.go`). The catch-up handler replays frames through the normal handlers with `catchUp=true`, which suppresses per-frame UI calls; accumulated state is flushed to the UI in one `CatchUpMessages` bulk update at the end so the UI doesn't replay hours of history.

### Group consensus

Group state is a replayable stack, not mutable rows: `groupCreation` (whose hash *is* the group ID) plus `updateGroup` frames applied in timestamp order, deconflicted by counting `confirmation` signatures (`chat/consensus_store.go`, `chat/canonical_stack.go`, `chat/group_state.go`). Never mutate group state directly — emit an update and call `reloadGroupConsensus` / `writeGroupConsensus`. `chat/consensus_test.go` covers the adversarial cases and is the best spec for the rules.

### Adding a new persisted frame type

`chat/frame_registry.go` is the single description of every persisted frame type. `frameSpecs` gives each one its table, whether it travels through the reference flow, whether it is worth dialling for, and its loader and offer functions; `catchUpOrder`, `typeTable`, `allowedCatchUpFrames`, `syncableTypes` and `specsByType` are all derived from it in `init()`. Adding a type is:

1. `chat/protocol.go` — the type constant, and a handler-map entry
2. `chat/database.go` — the model in `AutoMigrate`
3. Implement `broadcastable` (`getID`/`getType`/`getPayload`/`getScope`/`getDestination`/`getAuthor`/`getTimestamp`) and `getSavedAt` for `catchUpAble`
4. `chat/reference_offer.go` — the per-type offer query, which is where you decide who is *authorized* to receive these frames
5. `chat/frame_registry.go` — one `frameSpec` entry wiring the above together
6. `chat/catch_up.go` — a case in the catch-up handler if the UI needs a bulk update
7. `chat/encryption.go` — a `batchDeleteKey` case if the frame is deletable as part of clearing history

The position of a syncable entry in `frameSpecs` is its catch-up replay rank, so ordering matters: `groupCreation` before `updateGroup`, `device` before anything signed by one. `chat/frame_registry_test.go` fails if an entry is incomplete, if a syncable type has no handler, if the derived tables disagree with the registry, or if a known ordering dependency is violated — that suite is what stops a half-registered frame from working live and silently never syncing to offline devices.

Authorization on the sync path lives entirely in the offer queries. `handleReferenceRequest` regenerates the offer with `getReferenceOfferFor` and serves only the intersection of that with what the peer asked for, so the per-type loaders need no checks of their own and should not grow any — a loader that filters is a filter that the offer path doesn't know about.

### Encrypted devices

Encrypted devices store and relay frames without being able to read them. Frames are sealed with a per-frame DEK; the DEK is wrapped per recipient user via ECDH (`chat/encryption.go`), and only the UUID and type stay in the clear so the reference flow still works. Recipients are capped at 15 for large groups. Because an encrypted device doesn't know which device belongs to which user, it challenges peers to prove key ownership before offering references. Any new frame type that should reach encrypted devices must be in the encrypted handler map *and* be encryptable.

## Conventions

- **`log.Fatal` is a deliberate abort.** logrus is wired to `b.fatalShutdown` via `log.RegisterExitHandler`, so Fatal closes the database, removes the PID file, and exits. It is used for invariant violations (unknown scope, corrupt UUID in the DB, marshalling failures) — not for anything a peer can trigger. Peer-supplied badness is `Warn`/`Error` + drop the frame. Never call `log.Fatal` from inside `Shutdown()`; it deadlocks by design (see the comment in `chat/chat.go`).
- **SQLite runs in WAL mode with a 4-connection pool** (`chat/database.go`): the DSN is `?_journal_mode=WAL&_busy_timeout=10000&_txlock=immediate`. WAL lets readers run while a writer holds the write lock, which is what keeps one slow query from blocking every other engine call. `_txlock=immediate` is load-bearing — it takes the write lock at `BEGIN`, avoiding the `SQLITE_BUSY_SNAPSHOT` that a deferred transaction hits on upgrade and that the busy handler never retries. None of this makes a read-modify-write atomic: `database/sql` checks the connection out *per statement*, so two goroutines doing `SELECT` then `UPDATE` interleave, and with a pool >1 they now interleave in parallel. Only `db.Transaction` holds a connection across statements, and there is exactly one of those in the package (`chat/group.go`). The encrypted-device database (`chat/encrypted_device.go`) is still single-connection.
- **`b.referenceDatabase` is a second, in-memory gorm DB** used purely for reference-flow bookkeeping. Its `SetMaxOpenConns(1)` is load-bearing and must never be raised: the DSN is `file::memory:` with no `cache=shared`, and mattn/go-sqlite3 gives every *connection* a private database, so a second one would serve queries against an empty, unmigrated schema. For the same reason it must never get a `SetConnMaxLifetime` — recycling that connection destroys the data.
- **All Fyne widget mutation must be on the UI thread** — wrap in `fyne.Do` (async) or `fyne.DoAndWait` (sync). Engine callbacks arrive on arbitrary goroutines, so every `chat.UI` implementation in `ui/` does this.
- **Config directories** come from `config/`: `~/.bounce` on Linux, `os.UserConfigDir()/bounce` elsewhere, `/data/data/chat.bounce/bounce` on Android, and a temp dir under `testing.Testing()`. Encrypted devices use the `-encrypted` variants. Each holds `bounce.db`, `blobs/`, `tor/` (`router/` is Arti's `DataDir`, `keys/` holds the hidden-service keypair), `bounce-log.txt` (rotated), and `.pid` (single-instance lock).
- `DEBUG=true` enables debug logging and gorm warnings; `REPORT_CALLER=true` adds call sites.
- The protocol is not finalized and has no version negotiation — all clients must run the same release. Changing a frame's wire layout is a breaking change for the whole network.

## Android

`android/` is a Kotlin client in one process with one Go runtime. `android/goengine` is a gomobile binding exposing `chat.Engine` as typed methods, and Kotlin implements two Go interfaces (`EventSink`, `NotificationSink`) so the engine pushes events *into* Kotlin. The Activity and the foreground service share a process, so Kotlin calls the `.aar` directly — there is no IPC, no serialization of commands, and no polling. `android/README.md` has the details.

Re-run `make android-bind` whenever `chat/`, `config/`, `network/` or `goengine/` changes — the `.aar` is a build artifact and is not checked in. `android/goengine/doc.go` carries an equivalent `//go:generate` line; keep the two in step.

Two things to know before editing it: `applicationId` must stay `chat.bounce` because `config/directory.go` hardcodes `/data/data/chat.bounce/bounce`, and every bound call blocks the calling thread, which is why the whole engine is wrapped in `Dispatchers.IO`.

Three traps at the Kotlin/Go seam, each of which has cost real debugging time:

- **Go marshals a nil slice or map as `null`, not `[]` or `{}`.** Collections in the `@Serializable` mirrors need nullable value types (`Map<String, List<ReadReceipt>?>`), and the `Json` config needs `coerceInputValues = true`. One non-nullable field throws and discards the *entire* payload — a catch-up that silently delivers nothing looks exactly like a network problem, so it gets debugged in the wrong package.
- **The engine does not report read state back.** `MarkAll*MessagesAsRead` writes to SQLite and emits no event. The unread badge is derived from the `seen` flags on the messages `ChatRepository` holds in memory, so any path that marks a thread read must *also* call `ChatRepository.markThreadRead(threadId)` or the counter will not move.
- **Do local UI work before the engine call, never after.** Bound calls block, and multi-second stalls have been measured on real hardware. Dismissing a notification or clearing a badge after the call means nothing visibly happens until it returns — and in a `BroadcastReceiver` the `goAsync()` budget can expire first, so the local work never runs at all and the system kills the process.

`EngineClient.call` times every engine call and logs anything over `SLOW_CALL_MS` (100ms) under the `BounceEngineCall` tag — `adb logcat -s BounceEngineCall:W` is the fastest way to locate a stall. Read the *completion* timestamps rather than the durations: calls that all release within the same few milliseconds are one blocked operation with a queue behind it, not many slow ones.

This client replaced a Fyne one that ran two Go binaries in two processes and shuttled msgpack-over-base64 command blobs across AIDL. Android UI performance was the motivation (`docs/next_steps.md`) — Fyne text rendering in particular. Nothing of it remains in the tree, but `ui/` is still the Fyne desktop UI and still shares the `chat.Engine`/`chat.UI` interfaces with this client, so a change to either interface has two implementations to update.
