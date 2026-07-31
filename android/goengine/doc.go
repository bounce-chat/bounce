// Package goengine is the gomobile binding surface for the Bounce chat engine.
//
// It exists so a native Android UI can drive chat.Engine directly. It replaces
// the android/service + android/activity pair, which ran two Go runtimes in two
// processes and shuttled msgpack-over-base64 command blobs across AIDL. A Kotlin
// UI needs none of that: the Activity and the foreground Service live in one
// process, so Kotlin calls straight into this package and Go pushes events back
// through interfaces Kotlin implements.
//
// # Type rules
//
// gobind accepts only bool, int, int8/16/32/64, uint8, float32/64, string,
// []byte, error, *T where T is declared here, and interfaces declared here.
// Notably it rejects maps, every slice except []byte, structs by value, and
// *all array types* - which includes uuid.UUID, since that is [16]byte. That
// single exclusion is what makes ~59 of chat.Engine's 79 methods unbindable.
//
// Consequences, applied uniformly below:
//
//   - Every UUID crosses as a canonical hyphenated string. uuid.UUID implements
//     TextMarshaler, so this is also what encoding/json emits, and it is exactly
//     what java.util.UUID.toString() produces.
//   - Structured payloads cross as JSON, never msgpack: kotlinx.serialization
//     parses JSON natively, and JSON renders UUID map keys and values as strings
//     rather than as 16-byte blobs Kotlin would have to reassemble by hand.
//   - Raw image and file bytes never go through JSON. They cross as []byte, or
//     stay on disk and cross as a path.
//   - Go int maps to Java long, which is a surprising signature for small enum
//     values, so this package uses int32 explicitly where the range allows.
//
// # Errors
//
// Methods return error, which gobind renders as `throws Exception`. Note that
// for an (T, error) pair gobind drops T when the error is non-nil, so no method
// here returns meaningful data alongside an error.
//
// # Threading
//
// Every bound call is a synchronous JNI->cgo call that blocks the calling
// thread. Treat the whole of Engine as Dispatchers.IO on the Kotlin side.
// Sink callbacks run on Go-owned threads attached to the JVM, never on the
// Android main looper, and possibly several at once; see EventSink.
//
//go:generate gomobile bind -target=android/arm64,android/amd64 -androidapi 29 -javapkg chat.bounce -o ../app/libs/goengine.aar -ldflags=-checklinkname=0 github.com/bounce-chat/bounce/android/goengine
package goengine

import "github.com/bounce-chat/bounce/chat"

// BindingVersion is checked by the Kotlin layer at startup so an .aar that is
// out of step with the app fails loudly instead of as a NoSuchMethodError.
const BindingVersion = 1

// Frame type names accepted by MarkAsRead. Re-exported so Kotlin does not carry
// its own copies of these string literals.
const (
	TypeDirectMessage = chat.TypeDirectMessage
	TypeUpdateDM      = chat.TypeUpdateDM
	TypeGroupMessage  = chat.TypeGroupMessage
	TypeUpdateGroup   = chat.TypeUpdateGroup
	TypeGroupCreation = chat.TypeGroupCreation
	TypeUpdateUser    = chat.TypeUpdateUser
)

// MutedForever is the sentinel MutedUntil value meaning "never unmute".
const MutedForever = chat.MutedForever

// Limits the UI should enforce before calling into the engine.
const (
	MaximumNameLength        = chat.MaximumNameLength
	MaximumMessageCharacters = chat.MaximumMessageCharacters
)

// EmbeddedFileLimit is the size at or below which a file is embedded and
// fetched automatically by every client that can see it. Above it a file is
// seeded from wherever it already sits on the sender's disk and is only
// transferred when someone explicitly asks for it.
//
// Re-exported so the UI can tell "this will arrive on its own" from "the user
// has to ask for this" without hardcoding a number that would silently drift
// from the engine's.
const EmbeddedFileLimit = chat.EmbeddedFileLimit
