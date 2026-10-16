# Bounce Technical Design

This document will build Bounce up from basic components.  It's a high-level overview intended to familiarize you with the concepts behind Bounce.

## The Network

Bounce instances communicate with each other via an embedded mixnet.  Tor is used currently, but clients may be migrated to other networks in the future.  The network keys constitute the identity of a device, and must be persistent.  When connecting to a peer, the public key of the dialing device must be known by the peer.  Put another way, when someone dials you, you have to be able to obtain the public key of the device who dialed you.

## User Identities

A user has a few basic properties like a name and profile image, and a collection of devices they own known as a "Device Group".

### Device Groups 

A device group is a set of devices owned by the same person.  They are defined as a tree structure of mutual signatures of network keys.  This means that any device can add a new device to the group, and devices have to consent to be added.

For example:
1. A user creates their Bounce profile on their phone, and their device group consists of just one device, the phone
2. The user now wants to add their laptop to their profile, which has its own device keys
3. The phone signs the laptop's public key (consenting to add), and the laptop signs the phone's public key (consenting to be added)
4. Both of these device details can now be distributed along with this pair of signatures, to show that the device group now consists of two devices

The above process can repeat indefinitely, with the phone able to add another device, and the laptop now also able to add devices and extend the device group.  The device group that you own is known as your "sync devices".

### Revoking Devices

Any device can revoke another member of the device group by broadcasting a signed frame that does so.  Revoking a device does not revoke any other devices that device previously added.  Revoked devices are marked as revoked and lose the ability to do anything, but they are retained in the device group forever, as their keys might be necessary to validate the historic mutual signatures in a device group.

Losing control over device keys can destroy a device group.  If a bad actor steals a device they can revoke the legitimate devices in the group, and other users will not be able to tell that this was done by a bad actor.  If a device is stolen, a race occurs to revoke that device before it revokes you, potentially leading to an unrecoverable loss of control over your identity.  Such is the nature of distributed cryptographic systems.

## Frames

All communication between Bounce instances occurs by sending frames, defined as golang structs.  Frames do not have a request-response cycle, all communication is asynchronous and event-driven.  When a frame is received, it is sent to a corresponding handler function for processing.  Frames are encoded with msgpack, and sent on the wire via a simple Type-Length-Value binary protocol consisting of the frame type, size of payload, and msgpack payload.

## Scopes

Frames are distributed by broadcasting them to a scope via a basic gossip protocol.  The broadcast function loads all the devices that are in scope and writes the frame to any device that is online and has not already acknowledged the frame.  In some special cases frames are sent speificially to one device, for example, when a new device is joining an existing profile, it sends a secret to the device that is adding it. 

### Sync

The sync scope consists of your sync devices.  Frames that might be sent to your sync devices include messages to yourself, and settings that only impact your devices (like which chats you have muted, or your default message retention settings).

### User

The user scope contains all of your sync devices, as well as all of the devices belonging to one other user.  This is used for direct messages, as well as settings related to a DM (message retention, etc).

### Group

The group scope is used for group messages and contains all of the devices belonging to all of the users who are members of the group.

### GroupWithInvites

The GroupWithInvites scope extends the group scope to include the device groups of all of the users who have a pending invite.  This scope is used for all of the metadata about a group (see: Group Consensus), including its creation, any name or image changes, as well as changes to the list of invited and active members.  This allows users to see the group details before they join, but prevents them from having access to group messages until they join.

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

The reference offer is the first step in the reference flow.  Every time a device opens a socket to another device, it checks if it has any frames to offer that peer.  To generate a reference offer, a device checks every frame that the peer is authorized to have, and if there is no record of that peer acking the frame in the past, a reference to the frame is included in the reference offer.

### Reference Request

When handing a reference offer, a device checks each reference and sees if it has the frame in the database.  If it already has the frame, it includes the reference in an ack that gets sent back to the peer.  If it doesn't have the frame, it includes the reference in a reference request that gets sent back to the peer.

### Catch Up

When handling a reference request, a device iterates through the references and confirms that the requesting peer does indeed have the right to view the frame.  If so, each frames is collected into a slice on a catch up frame.  The frames are then sorted by the time the device first saved them locally, and this catch up frame is then sent to the requester.

When handling a catch up, each frame is sequentially processed by the same handler that would process it if it was received in real time on the wire.  However, when handlers know that a frame is part of a catchup, they forgo some calls into the UI.  The catch up handler keeps track of which UI states might need to be updated, and updates them in bulk after all of the frames are processed.  This prevents the UI from replaying everything that happened while the device was offline in sequence, and simply brings the UI up to speed all at once.

## Adding Users

Users add each other using the following flow:

1. One of the users generates a random secret that is valid for 5 minutes.  This is known as the "offer user".
2. The offer user displays this secret, along with the address of the device that is doing the displaying.  This information is scanned by the other user, which is known as the "requester user".
3. The requester user gets this information and connects to the address.  It sends an "add user request", which is a structure containing the secret, as well as the entire requester user's user structure (which includes their ID, name, device group, etc).
4. The offer user receives this request and ensures the secret is correct and not expired, then validates the user inside, making sure the device group is valid, the data doesn't conflict with any existing users, and that the request came from one of the devices in the requester user's device group.
5. The offer user accepts this request by sending an "add user request accepted" structure.  This structure contains the offer user's user structure, as well as the offer user's device's signature of a hash of the requester user's user structure that was provided in the add user request.
6. The requester user receives this, validates the offer user data, and users their device to sign a hash of the offer user data.  The requester then creates the final "add user" struct.  This struct includes both of the full user structures, both of the signatures, and both of the addresses of the devices that did the signing.  The requester user runs this add user through the add user handler, and broadcasts it to all of their devices and all of the offer users devices.

The add user struct that was created by this process contains proof that a device belonging to each user's device groups consented to the adding of the other user.  It can be shared with any device that is a member of either user's device group now or in the future, and so long as neither of the signing devices are revoked, it can be used to add the contact.  This design makes it possible for a device to add a user it has never seen before, without hearing about that user from one of its sync devices.  For example:

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

### Group Keys

Every group has a symmetric key chain that its members share, used to address and wrap the group's frames on encrypted devices (see below).  The chain is part of the consensus: the groupCreation carries the first secret, and some updateGroups carry a new one, so any device that can replay the stack and holds the user's ECDH key can derive the whole chain and every device agrees on it.

A key is derived from the previous key and a fresh random secret (`K_n = HKDF(K_{n-1}, s_n)`, with `K_0` derived from `s_0` alone).  The secret is never sent in the clear: the frame that introduces it carries an ephemeral ECDH public key and the secret wrapped once per user who should hold it, under a key agreed between the ephemeral key and that user's ECDH key, along with a hash of the secret so a member who later receives the key by another route can check it.  The group's state tracks the list of updates that introduced keys (the key epochs) and whether the current key is stale.

Keys rotate whenever the membership shrinks.  An updateGroup that removes a user, revokes an invite, or rejects an invite carries a new secret wrapped only for the members who remain, so the departing user cannot derive the new key even though they can read the update.  A member leaving on their own cannot wrap a secret for the others, so that update only marks the key stale; the next member to seal a group frame for an encrypted device first broadcasts an updateGroup of type RotateKey carrying the new secret.  Any member may rotate, and rotation is a soft rule: a member who has not yet seen a departure will seal under the old key, which is acceptable, and every honest client rotates before sealing once it has.

Two members may rotate concurrently.  Consensus orders the two updates, and both members end up with the same chain once they have both; in the meantime each has sealed frames under an intermediate key that is not on the final chain.  Those frames stay readable because readers also try the keys reachable by applying the recent secrets in the other orders.

A user invited to a group receives the entire chain so far in the invite, wrapped for them the same way, so they can read the group's history on encrypted devices the same way they receive it from other members.  A device that finds its chain incomplete (because it joined before learning some secret, or lost its database) asks another member for the keys with a groupKeyRequest, and a member answers with the chain wrapped for that user.

## Files

Distributing files is an important part of several features in Bounce.  Users and groups can have icons, and messages can have both image and file attachments.  Files are identified by UUID and are distributed as a hash list, each hash created from a chunk with a maximum size of 1MiB.  Small files (20MiB or less) are embedded in Bounce's blobs directory, and are automatically downloaded by clients.  This size also serves as the limit for user and group icons, and for images displayed in messages.  Larger files are "seeded" off disk where they are and are not copied into Bounce's blobs directory (except an android, where all files are copied into a Bounce-controlled directory as a temporary workaround).  These files are not automatically downloaded by clients.

### Chunk Offers

A chunk offer is a frame that indicates a device has a chunk, for the purposes of informing other clients where they can get the data from.  When a client distributes a file, they broadcast a chunk offer for each chunk in the file, and clients that want the file send back "chunk requests", asking the device with the data to send it over.  Once the clients receive the data, they broadcast their own chunk offers for the same chunk, indicating they now have the data as well.  Once a file is on several devices, new devices have options for where they can get chunks, and implement a strategy to parallelize requesting different parts of the file from different devices, in a process comparable to bittorrent.

Bounce clients attempt to keep two sockets open with each peer they connect to, and always limit chunk sending to one socket when multiple are available.  This prevents a chunk that is slow to send from blocking smaller messages that should remain low latency, like typing indicators and messages.

## Encrypted Devices

The goal with encrypted devices is for them to serve the role of a normal device, in that they are able to receive, broadcast, and reference frames, including file chunks, without being able to read any of the content of the frames or generate their own new frames.  The encrypted device should only have access to the minimum amount of metadata required to accomplish this.  This enables hosting instances of Bounce in less-trusted environments, such as VPSes, without concern that compromise of those hosts would result in a breach of confidentiality or loss of control over one's identity in Bounce.

### User Keys

To facilitate encrypted devices, each user has a persistent ECDH key.  An initial ECDH key is created when the user is created, and these keys are only rolled when the user revokes a device.

### Mailboxes

Frames on an encrypted device are addressed to mailboxes rather than to users.  Every conversation has a secret that only its participants hold: a user's own devices share a secret derived from the user's ECDH private key, two users share one derived from the ECDH agreement between their keys, and a group's members share the group's key chain.  A mailbox is a conversation secret together with a weekly epoch; its label is a pseudorandom value derived from the two, so labels rotate every week and nothing about a label reveals which conversation, or which users, it belongs to.

To store a frame on an encrypted device, a normal device generates a data encryption key (DEK) unique to that copy and seals the frame's type, ID and payload under it, bound to a random ID chosen for the copy.  It then adds one mailbox entry per conversation the frame belongs to, for the current epoch: the mailbox label, the DEK wrapped under a key derived from the same secret and epoch, and a tag derived from the secret and the frame's real ID.  A user's own devices derive every DM and group secret the user holds, so they read the user's frames through the same mailboxes as everyone else; the sync mailbox is only used for frames that have no conversation, such as drafts and device records.  The encrypted device stores the copy ID, the ciphertext, and the entries.  It learns nothing else: no public keys, no real frame ID, no type, no timestamp and no conversation.  It orders frames by when it received them.  The tag lets it recognise a frame it already holds when a second device relays the same frame under a different copy ID, so it keeps one copy, without being able to tell which frame that is.

Every copy also carries a slot and a version.  A host keeps at most one owner frame per slot: a copy with a newer version replaces the one held, and an older one arriving late is dropped.  Drafts are what this is for.  A device saves a new draft frame whenever the text changes and keeps only the newest per thread, so without slots a host would accumulate every version and replay all of them to a device catching up.  A draft's slot is derived from the user's sync secret and the thread, so all of the user's devices derive the same slot, nobody else can, and the host cannot tell which thread it is; its version is the draft's own timestamp, so devices that come online in any order converge on the newest draft without knowing of each other's copies.  Every other frame carries a random slot that never matches anything, purely so that drafts are not the only frames with one, which would let the host pick them out.

A frame sent to another user's encrypted device carries only the entries that user can read, the DM between the two users or the groups they share, so a host never holds entries for conversations its owner is not in and never learns how many conversations a frame fans out to.  A user's own encrypted devices get every entry, so the user's contacts can fetch from them while the user is offline.  A group frame is one entry whatever the size of the group, because every member derives the same key, which is what removed the old cap on recipients.

Reading is the reverse.  A device derives every mailbox it can read for the current epoch, the next one, and the previous thirteen (its sync mailbox, one per contact, one per key of every group it is in, and one per sibling device for re-keys), tells the encrypted device the labels with a mailboxRequest, and opens whatever comes back with the matching secret and epoch.  The request is re-sent whenever that set changes, so a host learns about a new contact, group or key shortly after the device does.  Possession of a label is the proof of membership; a label cannot be guessed and presents nothing but itself.  Frames older than the lookback window are out of reach of every device, which makes it the effective retention of an encrypted device.

Re-key frames are the one exception to conversation secrets: when a user rolls their ECDH key, the new key must not be readable with the key being replaced, so those frames are addressed to mailboxes derived from an agreement between two of the user's devices' own keys.

### Encrypted Reference Flow

For the purposes of scoping, normal devices can treat encrypted devices as any other device belonging to the user who owns it.  Normal devices offer references to, and send sealed copies of, any frame that would go to a regular device owned by the encrypted device's owner.

Encrypted devices do not know which devices belong to which users, only which labels each connected peer has asked for.  On every connection the normal device sends its mailboxRequest, and the encrypted device answers with a reference offer of every stored frame in those mailboxes it has not already delivered to that peer, by copy ID.  The normal device requests the ones it has no delivery record for, opens them, acks the copy IDs, and handles the frames as it otherwise would.  New frames that arrive while the peer is connected are pushed to it directly.  Because copy IDs are random, a device that already holds a frame may still fetch a copy of it from a host once; it recognises the duplicate once opened.

The encrypted device also needs to know which connected peers are its owner's own devices, because only the owner may store arbitrary frames there.  It sends an encryptedReferenceOfferChallenge, containing an ephemeral ECDH public key and random data.  The regular device does an ECDH exchange with this public key and its user private key, encrypts the data, and sends back its public key and the ciphertext.  A peer that proves it holds the owner's key is the owner.  The labels an owner asks for are remembered as the owner's mailboxes, and anyone else may only store frames addressed to one of those: that is how the devices of contacts the encrypted device has never met deliver to the owner, without the device learning who they are.  Those frames are stored in a separate, bounded tier so that strangers cannot fill the device.

### Managing Encrypted Devices

Encrypted device are owned and managed by a user.  There aren't many management actions right now, only re-keying the ownership key and clearing history.  Clearing a conversation's history on encrypted devices is expressed as the labels of that conversation's completed epochs before the clear time; the device deletes every frame and stored chunk in those mailboxes without knowing what conversation they were.  Frames of the current epoch are left until it completes.  Drafts used to be pruned by the owner listing which ones to keep, which would now name frames the device cannot see; instead every copy occupies a slot, described above, and only the newest draft of a thread survives.  An empty draft clears a thread and is kept like any other until the next draft replaces it, so a device that has been offline for a long time still learns the thread was cleared.

### Encrypted File Storage

When files are distributed, the file structure contains an encryption key, as well as an encrypted version of the hash list.  This means that any device with the file knows how to request and decrypt encrypted versions of the chunks that make up the file.

Encrypted devices do not have access to these file structures or chunk offers, and so they cannot know which chunks they should be requesting to store.  Therefore, file storage works in the other direction for encrypted devices.  Regular devices send encryptedChunkStorageRequests to encrypted devices, asking them to store a chunk of ciphertext and to distribute it to a set of mailboxes, the same labels the file's frames are addressed to.  Encrypted devices do so when the request comes from their owner or names one of the owner's mailboxes, and once they have the data they offer the encrypted chunk to any peer that has asked for one of those labels, via an encryptedChunkOffer.  Regular devices can use these encryptedChunkOffers along side regular chunkOffers when strategizing how to download a file.
