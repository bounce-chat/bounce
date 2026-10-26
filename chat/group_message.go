package chat

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var gmDeliveryNotificationMutex sync.Mutex
var gmDeliveryNotifications = map[ID]chan bool{}

// A group message is sent from a member of a group to a group
type groupMessage struct {
	SignedFrame
	ID               ID `gorm:"primary_key;"`
	Nonce            nonce
	SavedAt          int64 `msgpack:"-"`
	WrittenAt        int64
	DeleteAt         int64
	Seen             bool `msgpack:"-"`
	Undeliverable    bool `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Author           ID
	Destination      ID
	Text             string
	FileAttachments  []fileAttachment  `gorm:"foreignKey:MessageID"`
	ImageAttachments []imageAttachment `gorm:"foreignKey:MessageID"`
}

func (gm *groupMessage) getNonce() nonce {
	return gm.Nonce
}

func (gm *groupMessage) generateID() ID {
	ch := newContentHasher(typeGroupMessage, gm.Nonce).
		id(gm.Author).
		id(gm.Destination).
		int64(gm.WrittenAt).
		int64(gm.DeleteAt).
		bytes([]byte(gm.Text))
	packImageAttachments(ch, gm.ImageAttachments)
	packFileAttachments(ch, gm.FileAttachments)
	return ch.generate()
}

func (gm *groupMessage) BeforeCreate(tx *gorm.DB) error {
	if !idMatchesContent(gm) {
		return errors.New("group message ID is not generated from its content")
	}
	gm.SavedAt = time.Now().Unix()
	return nil
}

func (gm *groupMessage) AfterDelete(tx *gorm.DB) error {
	if gm.ID == NilID {
		return nil
	}
	err := deleteEach[imageAttachment](tx, "message_id = ?", gm.ID)
	if err != nil {
		return err
	}
	err = deleteEach[fileAttachment](tx, "message_id = ?", gm.ID)
	if err != nil {
		return err
	}
	err = tx.Where("frame_id = ? AND frame_type = ?", gm.ID, typeGroupMessage).Delete(&deliveryRecord{}).Error
	if err != nil {
		return err
	}
	return deleteEach[readReceipt](tx, "target = ? AND target_type = ?", gm.ID, typeGroupMessage)
}

func (gm *groupMessage) getID() ID {
	return gm.ID
}

func (gm *groupMessage) getScope(_ ID) int {
	return scopeGroup
}

func (gm *groupMessage) getDestination(_ ID) ID {
	return gm.Destination
}

func (gm *groupMessage) getType() uint16 {
	return typeGroupMessage
}

func (gm *groupMessage) getPayload() []byte {
	bytes, err := msgpack.Marshal(signedContainer{
		Payload:   gm.OriginalPayload,
		Signature: gm.Signature,
		Signer:    gm.Signer,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group message's signed container")
	}

	return bytes
}

func (gm *groupMessage) getAuthor() ID {
	return gm.Author
}

func (gm *groupMessage) getTimestamp() int64 {
	return gm.WrittenAt
}

func (gm *groupMessage) getSavedAt() int64 {
	return gm.SavedAt
}

func (gm *groupMessage) empty() bool {
	return strings.TrimSpace(gm.Text) == "" && len(gm.ImageAttachments) == 0 && len(gm.FileAttachments) == 0
}

func (b *Bounce) handleGroupMessage(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	groupMutex.Lock()
	defer groupMutex.Unlock()
	readReceiptMutex.Lock()
	defer readReceiptMutex.Unlock()

	// Verify and unpack the signed container
	sc, err := b.unpackSignedContainer(typeGroupMessage, payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for group message")
		return nil, false
	}
	var gm groupMessage
	err = msgpack.Unmarshal(sc.Payload, &gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group message")
		return nil, false
	}
	gm.OriginalPayload = sc.Payload
	gm.Signature = sc.Signature
	gm.Signer = sc.Signer

	// Make sure the ID is generated from the content
	if !idMatchesContent(&gm) {
		log.WithFields(log.Fields{
			"peer":       peer,
			"id":         gm.getID(),
			"content_id": gm.generateID(),
		}).Error("rejecting group message with an ID not generated from its content")
		return nil, false
	}

	// Ignore empty messages
	if gm.empty() {
		log.WithFields(log.Fields{
			"id": gm.ID,
		}).Warn("ignoring empty group message")
		go b.sendAck(peer, typeGroupMessage, gm.ID)
		return nil, false
	}

	// Ignore messages that are too long
	if utf8.RuneCountInString(gm.Text) > MaximumMessageCharacters {
		log.WithFields(log.Fields{
			"id": gm.ID,
		}).Warn("ignoring group message that is too long")
		go b.sendAck(peer, typeGroupMessage, gm.ID)
		return nil, false
	}

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", gm.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": gm.Signer,
			}).Error("signer device not found for group message")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"address": gm.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < gm.WrittenAt {
		log.WithFields(log.Fields{
			"id":     gm.ID,
			"signer": gm.Signer,
		}).Warn("ignoring group message signed by revoked device")
		go b.sendAck(peer, typeGroupMessage, gm.ID)
		return nil, false
	}

	// Make sure the device that signed this message belongs to the author
	if !b.signedByUser(sc, gm.Author) {
		log.WithFields(log.Fields{
			"id":     gm.ID,
			"group":  gm.Destination,
			"signer": sc.Signer,
			"author": gm.Author,
		}).Warn("received group message signed by a different user than the author, ignoring")
		return nil, false
	}

	// Ignore anything from a blocked user
	if blockedUser(gm.getAuthor()) {
		log.WithFields(log.Fields{
			"id":     gm.ID,
			"author": gm.getAuthor(),
		}).Warn("ignoring group message from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeGroupMessage, gm.ID)
			}
		}
		return nil, false
	}

	// If we have already seen this message, all we need to do is mark that this peer has the message and ack it
	var existingGM groupMessage
	err = b.database.Where("id = ?", gm.ID).First(&existingGM).Error
	if err == nil {
		return &existingGM, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up group message")
	}

	// Get the current state of the group
	gs, err := b.currentGroupState(gm.Destination)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": gm.Destination,
			"error":    err.Error(),
		}).Error("error getting group state while handling group message")
		return nil, false
	}

	// Make sure the author is in the group
	if !gs.isMember(gm.Author) {
		log.WithFields(log.Fields{
			"user":  gm.Author,
			"group": gm.Destination,
		}).Warn("user sent message to a group they are not in, ignoring")
		go b.sendAck(peer, typeGroupMessage, gm.ID)
		return nil, false
	}

	// If the message is older than the group's ClearBefore, don't process it
	if gm.WrittenAt < gs.clearBefore {
		log.WithFields(log.Fields{
			"user":       gm.Author,
			"group":      gm.Destination,
			"written_at": gm.WrittenAt,
		}).Debug("ignoring a group message that was written before the history was cleared")
		go b.sendAck(peer, typeGroupMessage, gm.ID)
		return nil, false
	}

	// Ignore messages that should have already been deleted
	if gm.DeleteAt != 0 && gm.DeleteAt <= time.Now().Unix() {
		log.WithFields(log.Fields{
			"id":          gm.ID,
			"author":      gm.Author,
			"destination": gm.getDestination(b.currentUserID()),
			"delete_at":   gm.DeleteAt,
		}).Debug("ignoring a group message that has delete time that occured before now")
		go b.sendAck(peer, typeGroupMessage, gm.ID)
		return nil, false
	}

	// Make sure the user has permission to post
	if gs.postingRestricted && !gs.isAdmin(gm.Author) {
		log.WithFields(log.Fields{
			"user_id": gm.Author,
		}).Warn("user attempted to post in a group without permission")
		return nil, false
	}

	// If this message created before we joined the group, mark it as already seen
	if gs.acceptedAt != 0 && gm.WrittenAt < gs.acceptedAt {
		gm.Seen = true
	}

	// Make sure the user interface isn't still displaying that the user is typing
	b.clearUserTypingIndicator(gm.Author, gm.Destination, typeGroupMessage)

	// If we wrote this message, assume we've seen it
	gm.Seen = gm.Author == b.currentUserID()

	// Save the new group message
	err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message")
	}

	// Expire the message on time if it has a retention
	if gm.DeleteAt != 0 {
		go b.deleteGroupMessageAt(gm.DeleteAt, gm.ID)
	}

	uiImageAttachments := []ImageAttachment{}
	for _, ia := range gm.ImageAttachments {
		uiImageAttachments = append(uiImageAttachments, ImageAttachment{
			ID:       ia.FileID,
			Name:     ia.Name,
			Size:     ia.Size,
			Width:    ia.Width,
			Height:   ia.Height,
			BlurHash: ia.BlurHash,
		})
	}
	uiFileAttachments := []FileAttachment{}
	for _, fa := range gm.FileAttachments {
		uiFileAttachments = append(uiFileAttachments, FileAttachment{
			ID:   fa.FileID,
			Name: fa.Name,
			Size: fa.Size,
		})
	}

	// Inform the UI about the new message
	if !catchUp {
		b.ui.DisplayGroupMessage(GroupMessage{
			ID:               gm.ID,
			Author:           gm.Author,
			Thread:           gm.Destination,
			WrittenAt:        gm.WrittenAt,
			SavedAt:          gm.SavedAt,
			ExpiresAt:        gm.DeleteAt,
			Seen:             gm.Seen,
			Text:             gm.Text,
			ImageAttachments: uiImageAttachments,
			FileAttachments:  uiFileAttachments,
		})

		if b.postNotification != nil && b.shouldNotifyGM(&gm) {
			title, content, err := b.getGMNotificationContent(&gm)
			if err == nil {
				b.postNotification(
					gm.ID.String(),
					title,
					content,
					gm.Destination.String(),
					b.getNotificationIcon(gm.Destination.String()),
				)
			}
		}

		if owner, ok := b.peerOwner(peer); ok {
			b.ui.MessageDelivered(gm.ID, owner)
		} else {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("group message received from unknown peer")
		}

		// Find any read receipts for this message that came early, add missing data, broadcast and send to the UI
		b.processEarlyReadReceipts(gm.ID, typeGroupMessage, true)

		// Update the activity timestamp on the group model
		b.updateLastGroupActivity(gm.Destination, gm.SavedAt)
	}

	return &gm, true
}

func (b *Bounce) shouldNotifyGM(gm *groupMessage) bool {
	if gm.Author == b.currentUserID() {
		return false
	}

	var g group
	err := b.database.Take(&g, "id = ?", gm.Destination).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting group for group message")
		return false
	}

	if g.MutedUntil == MutedForever {
		return false
	}

	if g.MutedUntil > time.Now().Unix() {
		return false
	}

	if waitingForInitialSyncFrom() != "" {
		return false
	}

	if anotherDeviceIsActive.Load() && !uiIsInForeground.Load() {
		return false
	}

	if uiIsInForeground.Load() && autoscrolling(gm.Destination) {
		return false
	}

	return true
}

func (b *Bounce) getGMNotificationContent(gm *groupMessage) (string, string, error) {
	var g group
	err := b.database.Take(&g, "id = ?", gm.Destination).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting group for group message")
		return "", "", err
	}

	var author user
	err = b.database.Take(&author, "id = ?", gm.Author).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting the author of a group message")
		return "", "", err
	}

	content := author.Name + ": " + gm.Text
	if len(gm.Text) == 0 {
		content = author.Name + " posted "

		if len(gm.ImageAttachments) > 1 {
			content += "images"
		} else if len(gm.ImageAttachments) == 1 {
			content += "an image"
		}

		if len(gm.FileAttachments) > 1 {
			if len(gm.ImageAttachments) != 0 {
				content += " and "
			}
			content += "files"
		} else if len(gm.FileAttachments) == 1 {
			if len(gm.ImageAttachments) != 0 {
				content += " and "
			}
			content += "a file"
		}
	}

	return g.Name, content, nil
}

// SendGroupMessage sends a message to a group.  Attachments that cannot be read or are too large to embed are left
// out and the rest of the message is still sent; an error means nothing was sent.
func (b *Bounce) SendGroupMessage(thread ID, text string, attachments []OutgoingAttachment) error {
	if utf8.RuneCountInString(text) > MaximumMessageCharacters {
		log.Error("refusing to send message that is too long")
		closeAttachments(attachments)
		return errMessageTooLong
	}
	gs, err := b.currentGroupState(thread)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": thread,
			"error":    err.Error(),
		}).Error("error getting group state while sending group message")
		closeAttachments(attachments)
		return err
	}

	now := time.Now()
	deleteAt := int64(0)
	retentionSeconds := gs.retention
	if retentionSeconds > 0 {
		deleteAt = now.Unix() + retentionSeconds
	}
	gm := &groupMessage{
		Nonce:       newNonce(),
		WrittenAt:   now.Unix(),
		Seen:        true,
		Author:      b.currentUserID(),
		Destination: thread,
		DeleteAt:    deleteAt,
		Text:        text,
	}

	// Attachments are part of the message's content, and publishing one sends a file that names the message, so
	// the attachments are prepared first, the message gets its ID, and only then are the files published
	scope, destination := scopeGroup, thread
	attached := b.prepareMessageAttachments(attachments, scope)
	gm.ImageAttachments = attached.images
	gm.FileAttachments = attached.files

	if gm.empty() {
		log.Error("refusing to send empty group message")
		return errMessageEmpty
	}

	gm.ID = gm.generateID()
	attached.stampMessageID(gm.ID)
	err = b.publishMessageAttachments(attached, destination, gm.ID)
	if err != nil {
		return err
	}

	gm.OriginalPayload, err = msgpack.Marshal(gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group message")
	}
	sc := b.createSignedContainer(typeGroupMessage, gm.OriginalPayload)
	gm.Signature = sc.Signature
	gm.Signer = sc.Signer

	err = b.database.Create(gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message")
	}
	b.updateLastGroupActivity(gm.Destination, gm.SavedAt)

	// Expire the message on time if it has a retention
	if gm.DeleteAt != 0 {
		go b.deleteGroupMessageAt(gm.DeleteAt, gm.ID)
	}

	go b.checkIfGroupMessageUndeliverableAt(now.Add(undeliverableAfter).Unix(), gm.ID)

	go b.ui.DisplaySentGroupMessage(GroupMessage{
		ID:               gm.ID,
		Author:           gm.Author,
		Thread:           gm.getDestination(b.currentUserID()),
		WrittenAt:        gm.WrittenAt,
		SavedAt:          gm.SavedAt,
		ExpiresAt:        gm.DeleteAt,
		Text:             gm.Text,
		Seen:             gm.Seen,
		Undeliverable:    gm.Undeliverable,
		ImageAttachments: attached.imageDTOs,
		FileAttachments:  attached.fileDTOs,
	})

	b.broadcast(gm)
	return nil
}

func (b *Bounce) deleteGroupMessageAt(timestamp int64, id ID) {
	// Wait until the message is due (a timer for a time already past fires at once), unless the engine shuts down
	// first, in which case the sweep at the next start deletes it
	if !b.sleepOrDone(time.Until(time.Unix(timestamp, 0))) {
		return
	}

	// Delete from the database
	err := deleteEach[groupMessage](b.database, "id = ?", id)
	if err != nil {
		log.WithFields(log.Fields{
			"message_id": id,
			"error":      err.Error(),
		}).Fatal("error deleting group message that expired")
	}

	// Delete from the UI
	b.ui.DeleteItem(id)
}

func (b *Bounce) checkIfGroupMessageUndeliverableAt(timestamp int64, id ID) {
	// Create a receiver for delivery notifications
	delivered := make(chan bool, 1)
	gmDeliveryNotificationMutex.Lock()
	gmDeliveryNotifications[id] = delivered
	gmDeliveryNotificationMutex.Unlock()

	// Create a timer that waits until the timestamp
	duration := time.Duration(timestamp-time.Now().Unix()) * time.Second
	sleeper := time.NewTimer(duration)
	defer sleeper.Stop()

	// If this message gets ack'd then just return here, otherwise wait until the timestamp
	select {
	case <-delivered:
		gmDeliveryNotificationMutex.Lock()
		delete(gmDeliveryNotifications, id)
		gmDeliveryNotificationMutex.Unlock()
		return
	case <-sleeper.C:
		gmDeliveryNotificationMutex.Lock()
		delete(gmDeliveryNotifications, id)
		gmDeliveryNotificationMutex.Unlock()
		break
	}

	// Check if the message still hasn't been delivered
	var count int64
	err := b.database.Model(&deliveryRecord{}).Where("frame_id = ? AND frame_type = ?", id, typeGroupMessage).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error counting delivery records for group message")
	}

	// If it hasn't been delivered, mark it as undeliverable
	if count == 0 {
		// Find the DM
		var gm groupMessage
		err = b.database.Where("id = ?", id).First(&gm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"message_id": id,
				}).Warn("no group message found when checking for delivery")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error looking up group message")
			}
		}

		// This message is undeliverable, update the undeliverable field and inform the UI
		err = b.database.Model(&gm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": gm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable group message")
		}
		b.ui.MarkMessageUndeliverable(gm.ID)
	}
}
