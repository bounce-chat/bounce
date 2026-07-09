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

Any device can revoke another member of the device group by broadcasting a signed frame that does so.  Revoking a device does not revoke any other devices that device previously added.  Revoked devices are marked as revoked and lose the ability to do anything, but they are retained in the device group forever, as their keys might be necessary to validate the historic mutual signatures in a device group.

Losing control over device keys can destroy a device group.  If a bad actor steals a device they can revoke the legitimate devices in the group, and other users will not be able to tell that this was done by a bad actor.  If a device is stolen, a race occurs to revoke that device before it revokes you, potentially leading to an unrecoverable loss of control over your identity.  Such is the nature of distributed cryptographic systems.

## Frames

All communication between Bounce instances occurs by sending frames, defined as golang structs.  Frames do not have a request-response cycle, all communication is asynchronous and event-driven.  When a frame is received, it is sent to a corresponding handler function for processing.  Frames are encoded with msgpack, and sent on the wire via a simple Type-Length-Value binary protocol consisting of the frame type, size of payload, and msgpack payload.

## Scopes

Frames can either be sent to one specific device, or broadcast to a scope.  For example, when a new device is joining an existing profile, it sends a secret to the device that is adding it.  That frame only needs to go to that one device.  However, the majority of frames are broadcast to a scope using a basic gossip protocol.  This broadcast function takes a look at all the devices that are in scope, and writes the frame to any device that is online and has not already acknowledged the frame.

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

The custom scope uses records in the database that define a specific set of devices.  This is used when the context for a scope is disappearing, but it is important to share a frame with certain devices.  For example, when a group is deleted, the group is removed from the database.  But the frame that deletes the group should be saved, and shared with any members of the group who might come online later and need to be informed.  This is accomplished by saving the devices that are in the group in a custom scope, deleting the group, and preserving the delete action with that custom scope.

## Peering

Each instance of Bounce attempts to connect to peers.  Bounce instances always attempt to dial all of their sync devices.  Users and groups are dialed if they have been interacted with in the last 30 days, or if there is data on that device for those users or groups (for example, you might not dial someone you haven't written in 3 months, but as soon as you have a pending message for them, you dial them).  When dialing a user or group, 4 random devices from that scope are selected to be dialed.  Bounce will continue to try other devices until there are 4 representatives of the scope connected, if possible.

## Delivery Tracking and References

In order to prevent re-delivering frames, Bounce keeps track of which devices have received a frame.  This is done by sending an ack frame back to the source of a frame after successfully handling it, and saving a delivery record when an ack is received.  Acks are always from one device to another, Bounce has no mechanism to indirectly learn if a device has a frame.  When two devices connect, both can search for frames that other should have but might not have.  After exchanging this list of IDs, both devices can look for frames they need from the other, and request them.  This allows two devices to only share data that the other doesn't have when syncing back up, and is known as the "reference flow", as devices pass around "references" (consisting of the UUID and type of a frame) to each other.

### Reference Offer

The reference offer is the first step in the reference flow.  Every time a device opens a socket to another device, it sends a reference offer, unless there are no frames to offer.  To generate a reference offer, a device checks every frame that the peer is authorized to have, and joins the delivery records table.  If there is no record of successful delivery to the peer, a reference to the frame is included in the reference offer.

### Reference Request

When handing a reference offer, a device checks each reference and sees if it has the frame in the database.  If it already has the frame, it includes the reference in an ack that gets sent back to the peer.  If it doesn't have the frame, it includes the reference in a reference request that gets sent back to the peer.

### Catch Up

When handling a reference request, a device iterates through the references and confirms that the requesting peer does indeed have the right to view the frame.  If so, each frames is collected into a slice on a catch up frame.  The frames are then sorted by the time the device first saved them locally, and this catch up frame is then sent to the requester.

When handling a catch up, each frame is sequentially processed by the same handler that would process it if it was received in real time on the wire.  However, when handlers know that a frame is part of a catchup, they forgo some calls into the UI.  The catch up handler keeps track of which UI states might need to be updated, and updates them in bulk after all of the frames are processed.  This prevents the UI from replaying everything that happened while the device was offline in sequence, and simply brings the UI up to date all at once.

## Adding Users

Users add each other using the following flow:

1. One of the users generates a random secret that is valid for 5 minutes.  This is known as the "offer user".
2. The offer user displays this secret, along with the address of the device that is doing the displaying.  This information is scanned by the other user, which is known as the "requester user".
3. The requester user gets this information and connects to the address.  It sends an "add user request", which is a structure containing the secret, as well as the entire requester user's user structure (which includes their ID, name, device group, etc).
4. The offer user receives this request and ensures the secret is correct and not expired, then validates the user inside, making sure the device group is valid, the data doesn't conflict with any existing users, and that the request came from one of the devices in the requester user's device group.
5. The offer user accepts this request by sending an "add user request accepted" structure.  This structure contains the offer user's user structure, as well as the offer user's device's signature of a hash of the requester user's user structure that was provided in the add user request.
6. The requester user receives this, validates the offer user data, and users their device to sign a hash of the offer user data.  The requester then creates the final "add user" struct.  This struct includes both of the full user structures, both of the signatures, and both of the addresses of the devices that did the signing.  The requester user runs this add user through the add user handler, and broadcasts it to all of their devices and all of the offer users devices.

The add user struct that was created by this process contains proof that a device belonging to each user's device groups consented to the adding of the other user.  It can be shared with any device that is a member of either user's device group now or in the future, and so long as neither of the signing devices are revoked, it can be used to add the contact.  This design makes it possible for a device to add a user it has never seen before, without hearing about that user from one of it's sync devices.  For example:

1. Offer user A owns a laptop and phone
2. Requester user B owns a desktop
3. A's laptop does the add user flow with B's desktop while A's phone is offline
4. A's laptop goes offline, then A's phone comes online, and still doesn't know anything about user B
5. User B's desktop can dial user A's phone now, and prove that user A's laptop added user B as a friend while the phone was offline

## Sending Messages

With the above out of the way, sending messages is straightforward.  A user creates a message structure containing the text of their message, as well as the IDs of any attachments, and signs it.  If it is a direct message, it is user-scoped to the counterparty of the direct message thread.  If it is a group message, it is group-scoped to the group.  The message is then broadcast and gossiped to any devices that are online, and any devices that are offline will pick it up via the reference flow when coming back online.  Upon seeing a message, users can broadcast read receipts, which are scoped and distributed in the same way.

## Group Consensus

The state of groups (including the group attributes, like the name and settings, as well as who is a member or invited, and the permission of the members) must be consistent across all devices with the same data, regardless of the order that data was received in.  This is accomplished via a process called "group consensus".

Groups are created via a frame called a "groupCreation", which defines the original state of the group, and changes to groups are accomplished via atmoic updates known as "updateGroups".  The ID of a group is derived from a hash of the original state as defined by the groupCreation, making it impossible for a client to lie about the original state of a group and still be referring to the same group.  Updates are applied in timestamp order, however relying just on the timestamps opens up a trivial vulnerability.  Consider the following situation:

1. Group G contains admins A and B
2. A decides to revoke B's admin status via an updateGroup, and broadcasts it
3. B sees this and immediately broadcasts an updateGroup that revokes A's admin status with an earlier timestamp

In a naive implementation, B always wins.  Bounce instances are eventually-consistent and need to be able to handle out of order data, so they have no way to tell that B is lying about the timestamp.

Bounce imperfectly solves this problem by having clients broadcast a "confirmation" when they see a valid updateGroup, which is simply a signature of the update.  When a conflicting update is encountered, the update with the later timestamp can still be considered the valid one if it has the majority of users confirming it and the earlier one does not.  This means that if A can get their revoke seen by a majority of users in the group before B broadcasts the conflicting "earlier" update, B will never be able to undo that change by sharing their conflicting update with an earlier timestamp.  Either way, all of the devices will share the same state of the group regardless of what happens.

Group consensus is implemented as a stack.  The stack begins with the group creation, then updates are applied in timestamp order.  If an update cannot be applied for any reason (permission issue, membership, etc), the stack is popped until the conflicting update is discovered.  The conflicting updates are compared, and the "earlier" one wins unless the "older" one has >50% of users confirming it and the earlier one does not.  Once all of the updates are pushed onto the stack and deconflicted this way, the stack contains the set of accepted canonical updates to the group, and the user broadcasts confirmations for any of these updates they have not already confirmed.  The current group state can then be determined by starting with the original state in the groupCreation, and applying all the valid updates in order.

## Files

Distributing files is an important part of several features in Bounce.  Users and groups can have icons, and messages can have both image and file attachments.  Files are identified by UUID and are distributed as a hash list, each hash created from a chunk with a maximum size of 1MiB.  Small files (20MiB or less) are embedded in Bounce's blobs directory, and are automatically downloaded by clients.  This size also serves as the limit for user and group icons, and for images displayed in messages.  Larger files are "seeded" off disk where they are and are not copied into Bounce's blobs directory (except an android, where all files are copied into a Bounce-controlled directory as a temporary workaround).  These files are not automatically downloaded by clients.

### Chunk Offers

A chunk offer is a frame that indicates a device has a chunk, for the purposes of informing other clients where they can get the data from.  When a client distributes a file, they broadcast a chunk offer for each chunk in the file, and clients that want the file send back "chunk requests", asking the device with the data to send it over.  Once the clients receive the data, they broadcast their own chunk offers for the same chunk, indicating they now have the data as well.  Once a file is on several devices, new devices have options for where they can get chunks, and implement a strategy to parallelize requesting different parts of the file from different devices, in a process comparable to bittorrent.

Bounce clients attempt to keep two sockets open with each peer they connect to, and always limit chunk sending to one socket when multiple are available.  This prevents a chunk that is slow to send from blocking smaller messages that should remain low latency, like typing indicators and messages.

## Encrypted Devices

The goal with encrypted devices is for them to serve the role of a normal device, in that they are able to receive, broadcast, and reference frames, including file chunks, without being able to read any of the content of the frames or generate their own new frames.  The encrypted device should only have access to the minimum amount of metadata required to accomplish this.  This enables hosting instances of Bounce in less-trusted environments, such as VPSes, without concern that compromise of those hosts would result in a breach of confidentiality or loss of control over one's identity in Bounce.

### User Keys

To facilitate encrypted devices, each user has a persistent ECDH key.  An initial ECDH key is created when the user is created, and these keys are only rolled when the user revokes a device.

### Encrypted Frames and Recipients

To encrypt a frame before sending it to an encrypted device, a normal device generates a new data encryption key (DEK) unique for that frame.  The frame is encrypted with the DEK.  Then, the device takes all of the users who are in scope to receive that frame.  For each user that is in scope, the device generates a key encryption key (KEK) from the ECDH exchange of their key and that user's key.  These KEKs are included as "recipients".  So, when storing a frame on an encrypted device, the unencrypted devices sends the encrypted frame, their public key, and the set of recipients, and the encrypted device checks to make sure that the owner of the encrypted device is among the recipients, then stores the data.  The only unencrypted data that the encrypted frame has in common with it's unencrypted origin in the UUID and Type, in order to facilitate the reference flow.

When receiving data from an encrypted device, devices only receive the encrypted frame, their KEK, and the public ECDH key of the user that did the encrypting.  Devices generate the DEK from their private ECDH key and the provided public ECDH key, then decrypt the frame and handle it as they otherwise would.

The above technique is only aware of user-level keys.  This does not scale well for massive groups, and to prevent unbounded linear growth in the number of recipients, they are capped to 15 randomly selected group members when groups are larger than that.  Creating ECDH keys on a group level and using group scoped recipients for them will be a future addition to the protocol, but must be well planned to account for how to roll keys in any of the possible group consensus situations.

Aside from encrypting frames to user ECDH keys, frames can also be encrypted to devices, which have ECDH keys only known to their owners.  This is a special case that is only used when users need to roll their keys, and need to securely send the new keys to the unrevoked devices via an encrypted device.

### Encrypted Reference Flow

For the purposes of scoping, normal devices can treat encrypted devices as any other device belonging to the user who owns it.  Normal devices offer references to, and send encrypted versions of, any frame that would go to a regular device owned by the encrypted device's owner.

Encrypted devices, however, do not know which devices belong to which users.  In order for them to send reference offers, they need to know which user-level key a device owns.  Encrypted devices do this by sending an encryptedReferenceOfferChallenge, containing an ephemeral ECDH public key and a random set of data.  The regular device must do an ECDH exchange with this public key and their private key, encrypt the data, then then send back their public key and the encrypted data.  The encrypted device can then confirm that the peer owns the private key for the provided public key, and send reference of any frames which include that public key as a recipient.

### Managing Encrypted Devices

Encrypted device are owned and managed by a user.  There aren't many management actions right now, only re-keying the ownership key, and pruning old drafts.  Because encrypted devices do not know the contents of the encrypted drafts, they don't know which ones are out of date, and draft storage can be pretty noisy.  So, devices regularly inform their encrypted devices of which draft frames should be kept, letting the encrypted device prune any draft frames that are not in that list.

### Encrypted File Storage

When files are distributed, the file structure contains an encryption key, as well as an encrypted version of the hash list.  This means that any device with the file knows how to request and decrypt encrypted versions of the chunks that make up the file.

Encrypted devices do not have access to these file structures or chunk offers, and so they cannot know which chunks they should be requesting to store.  Therefore, file storage works in the other direction for encrypted devices.  Regular devices send encryptedChunkStorageRequests to encrypted devices, asking them to store a chunk of ciphertext and to distribute it to a set of user keys.  Encrypted devices do so when their owner is one of the recipients, and once they have the data they offer the encrypted chunk to any of the authorized recipients, via an encryptedChunkOffer.  Regular devices can use these encryptedChunkOffers along side regular chunkOffers when strategizing how to download a file.
