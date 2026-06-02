# Bounce Technical Design

This document will build Bounce up from basic components

## The Network

Instances of Bounce need a way to communicate with each other, and use an embedded mixnet to do so.  All instances of bounce must use the same cryptographically-addressed network that is capable of implementing [this]() interface.  The network doesn't actually have to be a mixnet or decentralized, any cryptographically-addressed network could work, but a decentralized mixnet is necessary to fulfil Bounce's [goals]().  The network keys constitute the identity of a device, and must be persistent.  When connecting to a peer, the public key of the dialing device must be known by the peer.  Put another way, when someone dials you, you have to be able to obtain the public key of the device who dialed you.

## User Identities

Users are simply collections of devices owned by the same person.  A user has a few basic properties like a name and profile images, and a collection of devices known as a "Device Group".

### Device Groups 

A device group is a set of devices owned by the same person.  They are defined as a tree structure of mutual signatures of network keys.  This means that any device can add a new device to the group, and devices have to consent to be added.

For example:
1. A user creates their Bounce profile on their phone, and their device group consists of just one device, the phone
2. The user now wants to add their laptop to their profile, which has it's own device keys
3. The phone signs the laptop's public key (consenting to add), and the laptop signs the phone's public key (consenting to be added)
4. Both of these device details can now be distributed along with this pair of signatures, to show that the device group now consists of two devices

The above process can repeat indefinitely, with the phone able to add another device, and the laptop now also able to add devices and extend the device group.  The device group that you own is known as your "sync devices".

### Revoking Devices

Any device can revoke another member of the device group by broadcasting a signed frame that does so.  Revoking a device does not revoke any other devices that device previously added.  Revoked devices are marked as revoked and lose the ability to do anything, but they are retained in the device group forever, as their keys might be necessary to validate the mutual signatures in a device group.

## Frames

All communication between Bounce instances occurs by sending frames, defined as golang structs.  Frames do not have a request-response cycle, all communication is asynchronous and event-driven.  When a frame is received, it is sent to a corresponding handler function for processing.  Frames are encoded with msgpack, and sent on the wire via a simple Type-Length-Value binary protocol consisting of the frame type, size of payload, and msgpack payload.

## Scopes

Some frames are sent specifically to one device.  For example, when a new device is joining an existing profile, it sends a secret it obtained from the device that is adding it to the device that is adding it.  That frame only needs to go to one device.  However, the majority of frames are broadcast to a scope using a basic gossip protocol.  This broadcast function takes a look at all the devices that are in scope, and writes the frame to any device that is online and has not already acknowledged the frame.

### Sync

The sync scope consists of your sync devices.  Frames that might be sent to your sync devices include messages to yourself, and settings that only impact your devices (like which chats you have muted, or your default message retention settings).

### User

The user scope contains all of your sync devices, as well as all of the devices belonging to one other user.  This is used for direct messages, as well as settings related to a DM (message retention, etc).

### Group

The group scope is used for group messages and contains all of the devices belonging to all of the users who are members of the group.

### GroupWithInvites

The GroupWithInvites scope extends the group scope to include the device groups of all of the users who have a pending invite.  This scope is used for all of the metadata about a group (see: Group Consensus), including it's creation, any name or image changes, as well as changes to the list of invited and active members.  This allows users to see the group details before they join, but prevents them from having access to group messages until they join.

When a user with an invite accepts the invite and becomes a member, they go from being in the GroupWithInvites scope to being in the Group scope for that group, and as a result can see all of the historic messages for the group.

### Global / Overlap

The global scope is used to share profile updates with all known contacts and includes all devices associated with known users.  When a user updates their name or profile picture, this is broadcast using the global scope.  When a user receives someone else's globally scoped updated, the user broadcasts it to the best of their ability using the Overlap Scope.  The overlap scope is defined as all devices belonging to users who share a group with the user in question.  This way, if user A changes their name, and you know user A is in a group with user B, you know it's safe to send that update to user B, since they will necessarily be in user A's global scope.  Both you and user A might be friends with user C, but if you don't share a group you can't be certain of that, and so you do not share the update with user C.

### Custom

The custom scope uses records in the database that define a specific set of devices.  This is used when the context for a scope is disappearing, but it is important to share a frame with certain devices.  For example, when a group is deleted, the group is removed from the database.  But the frame that deletes the group should be saved, and shared with any members of the group who might come online later and need to be informed.  This is accomplished by saving the devices that are in the group in a custom scope, then deleting the group, then preserving the delete action with that custom scope.

## Delivery Tracking and References

In order to prevent re-delivering frames, Bounce keeps track of which devices have received a frame.  This is done by sending an ack frame back to the source of a frame after successfully handling it, and saving a delivery record when an ack is received.  Acks are always from one device to another, Bounce has no mechanism to indirectly learn if a device has a frame.  When two devices connect, both can search for frames that other should have but might not have.  After exchanging this list of IDs, both devices can look for frames they need from the other, and request them.  This allows two devices to only share data that the other doesn't have when syncing back up, and is known as the "reference flow", as devices pass around "references" (consisting of the UUID and type of a frame) to each other.

### Reference Offer

The reference offer is the first step in the reference flow.  Every time a device opens a socket to another device, it sends a reference offer, unless there are no frames to offer.  To generate a reference offer, a device checks every frame that the peer is authorized to have, and joins the delivery records table.  If there is no record of successful delivery to the peer, a reference to the frame is included in the reference offer.

### Reference Request

When handing a reference offer, a device checks each reference and sees if it has the frame in the database.  If it already has the frame, it includes the reference in an ack that gets sent back to the peer.  If it doesn't have the frame, it includes the reference in a reference request that gets sent back to the peer.

### Catch Up

When handling a reference request, a device iterates through the references and confirms that the requesting peer does indeed have the right to view the frame.  If so, the frames is marshaled and collected into a slice on a catch up frame.  The frames are then sorted by the time the device first saved them locally, and this catch up frame is then sent to the requester

When handling a catch up, each frame is sequentially processed by the same handler that would process it if it was received in real time on the wire.  However, when handlers know that a frame is part of a catchup, they forgo some calls into the UI.  The catch up handler keeps track of which UI states might need to be updated, and updates them in bulk after all of the frames are processed.

## Adding Users

Users add each other using the following flow:
1. One of the users generates a random secret that is valid for 5 minutes.  This is known as the "offer user".
2. The offer user displays this secret, along with the address of the device that is doing the displaying.  This information is scanned by the other user, which is known as the "requester user".
3. The requester user gets this information and connects to the address.  It sends an "add user request", which is a structure containing the secret, as well as the entire requester user's user structure (which includes their ID, name, device group, etc).
4. The offer user receives this request and ensures the secret is correct and not expired, then validates the user inside, making sure the device group is valid, the data doesn't conflict with any existing users, and that the request came from one of the devices in the requester user's device group.
5. The offer user accepts this request by sending an "add user request accepted" structure.  This structure contains the offer user's user structure, as well as the offer user's device's signature of a hash of the requester user's user structure that was provided in the add user request.
6. The requester user receives this, validates the offer user data, and users their device to sign a hash of the offer user data.  The requester then creates the final "add user" struct.  This struct includes both of the full user structures, both of the signatures, and both of the addresses of the devices that did the signing.  The requester user runs this add user through the add user handler, and broadcasts it to all of their devices and all of the offer users devices.

The add user struct that was created by this process contains proof that a device belonging to each user's device groups consented to the adding the other user.  It can be shared with any device that is a member of either user's device group now or in the future, and so long as neither of the signing devices are revoked, it can be used to add the contact.  This design makes it possible for a device to add a user it has never seen before, without hearing about that user from one of it's sync devices.  For example:

1. Offer user A owns a laptop and phone
2. Requester user B owns a desktop
3. A's laptop does the add user flow with B's desktop while A's phone is offline
4. A's laptop goes offline, then A's phone comes online, and still doesn't know anything about user B
5. User B's desktop can dial user A's phone now, and prove that user A's laptop added user B as a friend while the phone was offline

## Sending Messages

## Group Consensus

## Files

## Encrypted Devices

---
