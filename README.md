<p align="center">
  <img width=50% src="ui/assets/logo.png"/>
</p>

<p align="center">
Bounce is a distributed group chat application that protects metadata, with a familiar look and feel.
</p>

<p align="center">
  <img src="docs/screenshots.png"/>
</p>

## Design Overview

Each instance of Bounce includes a Tor hidden service, and all connections between devices occur over Tor.  Users can own multiple devices, including encrypted devices that do not have access to the message contents.  Users add contacts by scanning a code on the other person's device, or by meeting them in a group, there is no global namespace of all users.  Messages are scoped to their user or group, and gossiped between devices that are in scope and online.  Devices coming online have an efficient way to catch up.  Group states (name, admin status, etc) can survive a bad actor attempting to mess with history, so long as the majority of users are honest.  The UI is built using [Fyne](https://github.com/fyne-io/fyne).  For more details, see the [goals](docs/goals.md) and [design document](docs/design.md).

## Status

Things should be working pretty reliably, though some performance optimizations are still required, especially in the Android UI.  Please open an issue if a feature in the UI is not working as expected.  Bounce has not been audited by a third party yet, and should be considered experimental.

|Client|Status|Notes|
|---|---|---|
|Linux|✅|Fully supported|
|macOS|✅|Fully supported|
|Android|✅|Fully supported|
|Windows|❌|Requires an updated go-libtor build for windows, which has not been done yet.|
|iOS|⛔|iOS does not allow apps to run in the background.  Implementing Bounce on iOS will require a light client that receives notifications from another instance of Bounce via APNs, and reaches out to that instance to send messages.  This has not been planned yet.|

## Installation

### Binary Releases

* Visit the [Releases](https://github.com/bounce-chat/bounce/releases/) page to download the latest binaries.
* Arch users can find [bounce]() in the AUR.
* The best way to install Bounce on Android is with [Obtanium](https://github.com/ImranR98/Obtainium).

### Building from source

#### Prerequisites

Building Bounce from source currently requires cloning down forks of [fyne](https://github.com/bounce-chat/fyne) and [fyne-io/tools](https://github.com/bounce-chat/tools).  The relative directory structure should look like:

```
./bounce-chat/bounce  <-- you are here
./bounce-chat/fyne
./bounce-chat/tools
```

#### Desktop

Development builds of the desktop app can simply be built with `go build`.  The first build will take a while as it compiles [go-libtor](https://github.com/bounce-chat/go-libtor).

#### Android

Android is built on linux and requires `dex2jar`, `gobind` from go mobile, and `gradle`.

The forked fyne tools binary must be built: `cd bounce-chat/tools/cmd/fyne && go build`

Run `make android` to create `Bounce.apk`, a debuggable development build.

## License

Copyright 2026 Hayden Parker.  Bounce is licensed under the MIT license unless otherwise specified.  Design assets are licensed under CC BY-NC-ND.
