package chat.bounce.data

/**
 * How far an outgoing message has got.
 *
 * Derived entirely from state the engine already persists - `DeliveredTo` and
 * `ReadReceipts` on the message - so these survive a restart rather than only
 * reflecting live events seen this session. `DeliveredTo` holds *user* IDs, not
 * device IDs: the engine resolves each delivery record's destination address to
 * its owner (`chat/database.go:643`), which is what makes the distinction
 * between our own devices and everyone else's possible here.
 *
 * Ordered weakest to strongest. The states are a high-water mark, not a
 * sequence - a message reaches whichever is the strongest true statement about
 * it, and can skip past ones it never satisfied. That matters in practice:
 * if the recipient is online while our own second device is not, a message goes
 * straight from [OnDevice] to [DeliveredToOthers] and never shows
 * [SyncedToMyDevices] at all.
 */
enum class DeliveryState {
    /**
     * Written, but no device anywhere has acknowledged it. On a single-device
     * profile this is also where a message sits until the recipient acks, since
     * a device never acknowledges its own message.
     */
    OnDevice,

    /**
     * Another device *we* own has it. Only reachable with more than one device
     * on the profile - see [OnDevice].
     */
    SyncedToMyDevices,

    /**
     * A device belonging to somebody else has it.
     *
     * Note this includes encrypted store-and-forward devices, which hold
     * ciphertext they cannot read: the engine maps such a device to its owner's
     * user ID like any other, so a message parked on the recipient's relay
     * counts as delivered to them even though no human has seen it.
     */
    DeliveredToOthers,

    /** Somebody other than us has read it. */
    ReadByOthers,

    /**
     * Undeliverable. Overrides everything above - the engine sets this from its
     * own timer, independent of how far the message previously got.
     */
    Undeliverable,
}

/**
 * The strongest true statement about [message].
 *
 * Group threads use "any" semantics throughout: one member receiving or reading
 * is enough. "All" would need the member list to diff against and would
 * realistically never light up in a large group.
 *
 * Read receipts are optional per conversation
 * (`OverrideReadReceiptSetting`/`ReadReceiptsEnabled`), so a recipient who has
 * turned them off leaves a message at [DeliveryState.DeliveredToOthers]
 * permanently. That is correct, not a stuck icon.
 */
fun deliveryStateOf(
    message: ConversationItem.Message,
    currentUserId: String,
): DeliveryState {
    if (message.undeliverable) return DeliveryState.Undeliverable

    // Safe before the profile has loaded: a receipt on a message we sent is from
    // somebody else by definition, so this cannot over-report.
    if (message.readReceipts.any { it.actor.isNotBlank() && it.actor != currentUserId }) {
        return DeliveryState.ReadByOthers
    }

    // Everything below turns on telling our own user ID from anyone else's, and
    // currentUserId is empty until the profile loads. Reporting the weakest
    // state for that window is right; the alternative reads every delivery,
    // including syncs to our own devices, as a delivery to the recipient.
    if (currentUserId.isBlank()) return DeliveryState.OnDevice

    if (message.deliveredTo.any { it != currentUserId }) return DeliveryState.DeliveredToOthers
    // Nothing here is anyone else's, so anything at all is one of ours.
    if (message.deliveredTo.isNotEmpty()) return DeliveryState.SyncedToMyDevices

    return DeliveryState.OnDevice
}
