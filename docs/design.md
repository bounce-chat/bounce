# Bounce Technical Design

This document will build Bounce up from basic components

## The network

Instances of Bounce need a way to communicate with each other, and use an embedded mixnet to do so.  All instances of bounce must use the same cryptographically-addressed network that is capable of implementing [this]() interface.  The network doesn't actually have to be a mixnet or decentralized, any cryptographically-addressed network could work, but a decentralized mixnet is necessary to fulfil Bounce's [goals]().  The network keys constitute the identity of a device, and must be persistent.  When connecting to a peer, the public key of the dialing device must be known by the peer.  Put another way, when someone dials you, you have to be able to obtain the public key of the device who dialed you.

## User identities

Users are simply collections of devices owned by the same person.

### Device Groups 

### Adding and Revoking Devices

## Frames

### Wire Protocol

### Handlers

## Scopes

### Sync

### User

### Group

### Global / Overlap

## Delivery Tracking and References

### Acks

### Reference Offer

### Reference Request

### Catch Up

## Adding Users

## Group Consensus

## Files

## Encrypted Devices
