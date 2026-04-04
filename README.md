<p align="center">
  <img width=50% src="ui/assets/logo.png"/>
</p>

<p align="center">
Bounce is a distributed group chat application that protects metadata, while having a familiar look and feel.
</p>

<p align="center">
  <img src="docs/screenshots.png"/>
</p>

## Design Overview

Each instance of Bounce includes a Tor hidden service, and all connections between devices occur over Tor.  Users can own multiple devices, including encrypted devices that do not have access to the message contents.  Users add contacts by scanning a code on the other person's device, there is no global namespace of all users.  Messages are scoped to their user or group, and gossiped between the online device that are in scope.  Devices coming online have an efficient way to catch up.  Group states (name, admin status, etc) can survive a bad actor attempting to mess with history, so long as the majority of users are honest.  The UI is built using [Fyne](https://github.com/fyne-io/fyne).  For more details, see the [goals](docs/goals.md) and [design document](docs/design.md).

## Status

Things should be working pretty reliably!  Please open an issue if a feature in the UI is not working as expected.  While it is unlikely that existing protocol will change, I have not yet committed to freezing the existing protocol, so backwards compatibility between updates is not yet guaranteed.  Bounce has not been audited by a third party yet, and should be considered experimental.

|Client|Status|Notes|
|---|---|---|
|Linux|✅|Fully supported|
|macOS|✅|Fully supported|
|Android|✔️|Supported, but currently does not support backgrounding and will go offline and miss notifications shortly after being backgrounded on most devices.  See [Fyne#5221](https://github.com/fyne-io/fyne/discussions/5221).|
|Windows|❌|Requires an updated go-libtor build for windows, which has not been done yet.|
|iOS|⛔|iOS does not allow apps to run in the background.  Implementing Bounce on iOS will require a light client that receives notifications from a server via a message bus, and sends messages via that server.  This is not currently planned.|

## Installation

**Binary Releases**



**Building from source**



## License

Copyright 2026 Hayden Parker.  Bounce is licensed under the MIT license unless otherwise specified.  Design assets are licensed under CC BY-NC-ND.
