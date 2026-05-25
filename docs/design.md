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

### Group

### Global / Overlap

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
