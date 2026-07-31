# android

A native Kotlin + Jetpack Compose Android client for Bounce, replacing the Fyne
UI in `android/`.

## Why this exists

The Fyne Android build ran the app as **two Go runtimes in two processes**:

```
Fyne UI (activity process)  --AIDL-->  chat engine (:service process)
   aidlBounce shim                        eval.go 77-arm string switch
   msgpack -> base64 -> Java String        500ms poll for events
```

Every call was encoded as `map[int][]byte` -> msgpack -> base64 -> a Java
`String`, dispatched by a stringly-typed switch with no `default` arm, and every
UI update waited on a 500 ms polling tick. Errors and successful results shared
one `String` channel, where `""` meant success — so a misspelled command was
indistinguishable from a working one. (`SetDMLastOpened` and
`SetGroupLastOpened` shipped broken for exactly this reason: the arguments were
written into the wrong slots, and nothing could catch it.)

A native UI removes the reason for all of it. There is no second Go runtime, so
the Activity and the Service share one process and Kotlin calls the engine
directly.

## Architecture

```
                    one process, one Go runtime
  ┌──────────────────────────────────────────────────────────────┐
  │  Compose UI  ──►  ChatRepository  ──►  EngineClient (suspend) │
  │       ▲                  ▲                     │              │
  │       │            EngineEventPump             │ JNI          │
  │       │                  ▲                     ▼              │
  │       │           EngineBridge  ◄────  goengine.aar (Go)      │
  │       │          (EventSink +                  │              │
  │  StateFlow       NotificationSink)         chat.Engine        │
  │                          │                     │              │
  │                    MessageNotifier         Tor network        │
  └──────────────────────────────────────────────────────────────┘
        BounceService holds the foreground lifetime around all of it
```

- **`goengine/`** (Go) — the gomobile binding. Typed methods in, a push sink
  out. See its package doc for the type rules that shape it.
- **`EngineBridge`** implements the two Go interfaces. Go calls *into* Kotlin
  here, on Go-owned threads, so each method does one `Channel.trySend` and
  returns.
- **`EngineEventPump`** is the single ordered consumer of that channel.
- **`BounceService`** is a lifetime anchor, not an IPC endpoint. It exists to
  keep the process alive so messages arrive; the UI never talks to it.

## Building

```bash
make android            # bind the engine + assembleDebug -> Bounce.apk
make android-install    # the above, then adb install
make android-release    # signed release build (reads keystore vars from .env)
make android-bind       # regenerate app/libs/goengine.aar only
```

`make android-bind` must be re-run whenever anything under `chat/`,
`config/`, `network/` or `goengine/` changes — the `.aar` is a compiled
artifact and is not checked in.

Overridable: `ANDROID_SDK`, `ANDROID_NDK` (defaults to the highest installed),
`NATIVE_ABIS` (default `android/arm64,android/amd64` — arm64 for phones, amd64
so it runs on an emulator), `NATIVE_API`.

The first bind is slow: it compiles go-libtor, including Tor and OpenSSL, for
each ABI. Each `libgojni.so` is ~33 MB.

## Constraints worth knowing before changing anything

- **`applicationId` must stay `chat.bounce`.** `config/directory.go` hardcodes
  the engine's data directory to `/data/data/chat.bounce/bounce`. Change the id
  and the engine writes somewhere the app cannot reach.
- **AGP 9 supplies Kotlin itself.** Applying `org.jetbrains.kotlin.android` is a
  hard build error, which is why it is absent from the version catalog.
- **Every engine call blocks.** They are synchronous JNI→cgo calls with no async
  form. `EngineClient` puts all of them on `Dispatchers.IO`; the one exception,
  `blobPath`, is documented as pure string concatenation.
- **A running engine cannot necessarily dial.** `chat.Open` starts Tor on a
  goroutine and returns immediately, so `EngineHolder.client` is non-null within
  milliseconds while `TorNetwork.tor`/`.onion` stay nil until the bootstrap and
  hidden-service publish finish — a minute or more on a first run. `Dial` in
  that window does not wait; it returns
  `"cannot dial while network is not started"` *instantly*, which surfaces as a
  misleading "could not connect to device". Gate anything that dials
  (`requestToSync`, `requestToAddUser`, `requestToManageEncryptedDevice`) on
  `awaitNetworkOnline()`, never on a non-null client.
- **Sink callbacks must never block.** They run on Go threads with the emitting
  goroutine parked. Blocking on a lock that a thread inside an engine call holds
  deadlocks across JNI, and nothing on the Kotlin side will diagnose it.
- **No Google Play Services.** Bounce is installed via Obtainium/F-Droid by
  people who often do not have them, so QR scanning uses ZXing + CameraX rather
  than ML Kit.
- **UUIDs are strings everywhere.** `uuid.UUID` is `[16]byte`, which gobind
  rejects outright; the canonical hyphenated form is what both `uuid.Parse` and
  `java.util.UUID.toString()` speak.

## Icons

Both are vectors hand-converted from `ui/assets/icon.svg`, with paths and
gradient stops taken verbatim, so the mark matches the desktop client.

**Launcher** (`ic_launcher_foreground.xml`, and `ic_launcher_monochrome.xml`
which must keep identical geometry). The sizing is the non-obvious part.
`ui/assets/launcher_circle.png`, which the Fyne build shipped, places the mark at
**72.4% of its canvas** — and for a legacy icon that whole canvas is visible.

An adaptive icon only reliably shows the inner 72 of its 108dp, so matching that
apparent size means `0.724 × 72 ≈ 52` units — a scale of `52.1/400 = 0.1303`
against the artwork's viewBox. `ic_launcher_monochrome.xml` (themed icons) must
keep identical geometry, and is deliberately a separate file from the status-bar
icon: that one is framed for a 24dp viewport and stretching it into this 108dp
layer reproduces the over-zoom.

**Notification** (`ic_notification.xml`). All three shapes, flat white; Android
masks it to a silhouette and tints it. The gap between the fan and the wave is
genuine empty space between two non-touching paths, not a white shape painted
over them, so the union does not merge into a blob — verified against
`ui/assets/adaptive_icon.png`, which has the same three disconnected regions.
Dropping a shape to "avoid merging" just produces a visibly incomplete mark.

This one drawable feeds two very different renderings, which is the trap: the
status bar draws it small and white in a flat 24dp box, while from Android 12
the shade draws it tinted inside a **circular badge** — and that badge is the
large colour icon beside the app name, *not* the launcher icon. Changing
`ic_launcher_foreground.xml` has no effect on it.

Size follows from the badge. The mark is 400×381.7, so a copy `w` wide has a
half-diagonal of `0.691w`; at the launcher's 72.4% (`w = 17.4`) that is exactly
12 — the inscribed circle's radius — so its corners land on the mask edge. The
launcher survives the same proportion because its mark sits on an opaque white
disc filling the mask; a bare silhouette touching the edge reads as cropped.
**62.5%** (`scale = 0.0375`) puts the corners at 86% of the radius.

## Debugging

The Go engine logs to logcat under the tag `BounceGo` (see
`goengine/log_android.go` — `gomobile bind` does not link x/mobile's
`mobileinit`, so without that shim logrus writes to a descriptor nothing on
Android reads, and the entire engine is silent).

```bash
adb logcat -s BounceGo:V BounceEngine:V BounceService:V BounceOnboarding:V
```

The engine also keeps its own rotating copy on the device, which survives a
crash and covers everything from before the app was attached:

```bash
adb exec-out run-as chat.bounce cat /data/data/chat.bounce/bounce/bounce-log.txt
```

Set `DEBUG=true` in the engine's environment for debug-level logging.

## Notifications

Built on `NotificationCompat.MessagingStyle` + `Person` + long-lived
conversation shortcuts, so conversations appear in Android's Conversations
section with per-conversation settings.

The engine's `postNotification(id, title, content, openThread, icon)` is too
thin for this on its own: for a group message it concatenates the sender name
into the body, carries no timestamp, and does not say whether the thread is a
group. Rather than parse that back out — the body can contain a colon, and the
attachment-only fallback has no separator at all — notifications and UI events
travel the **same ordered channel**. The engine emits `DisplayGroupMessage`
with the full structured message immediately before it calls
`postNotification` for that message, so by the time the pump handles the
notification the message is already in the repository, with its author,
timestamp and attachments intact.

That keeps the *decision* to notify in the engine, where it belongs — mute
state, foreground state and cross-device DND are all engine concerns and are
not reimplemented here.

Notification ids are keyed on the **conversation**, never the message, so
`notify()` updates a conversation in place instead of stacking one notification
per message.

## Not done yet

- **Bubbles.** They need `MainActivity` to declare `resizeableActivity`,
  `allowEmbedded` and `documentLaunchMode="always"`, which realistically means a
  separate lightweight conversation activity. The notification code deliberately
  does not set `BubbleMetadata` rather than ship a silently-dropped one.
- **Inline images in notifications.** `MessagingStyle.Message.setData` needs a
  `content://` URI, which means exporting decrypted attachment bytes through the
  FileProvider — worth a deliberate decision about writing plaintext
  attachments to a shared surface before doing it.
- **Per-member avatars in group notifications.** The engine keys its
  notification-icon cache by thread, so a group notification currently carries
  the group avatar for every sender.
