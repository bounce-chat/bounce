# Bounce Technical Design

This document will build Bounce up from basic components

## 1. The network

Instances of Bounce need a way to communicate with each other, and use an embedded mixnet to do so.  All instances of bounce must use the same cryptographically-addressed network that is capable of implementing [this]() interface.  The network doesn't actually have to be a mixnet or decentralized, any cryptographically-addressed network could work, but a decentralized mixnet is necessary to fulfil Bounce's [goals]().  The network keys must be persistent, and constitute the identity of a device.  When connecting to a peer, the persistent network public key of the client must be known by the peer.  Put another way, when someone dials you, you have to be able to tell the public key of the device who dialed you.

## 2. User identities

Users are simply collections of devices owned by the same person.

### a. Device Groups 

### b. Adding and Revoking Devices
