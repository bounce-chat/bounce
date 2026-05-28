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

### Acks

### Reference Offer

### Reference Request

### Catch Up

## Adding Users

## Sending Messages

## Group Consensus

## Files

## Encrypted Devices

---
