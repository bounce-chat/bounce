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

var directMessageMutex sync.Mutex

var dmDeliveryNotificationMutex sync.Mutex
var dmDeliveryNotifications = map[ID]chan bool{}

// A direct message is a chat message from one user to another
type directMessage struct {
	SignedFrame
	ID               ID `gorm:"primary_key;"`
	Nonce            nonce
	SavedAt          int64 `msgpack:"-"`
	WrittenAt        int64
	DeleteAt         int64
	Seen             bool `msgpack:"-"`
	Undeliverable    bool `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Author           ID
	Xor              ID // XOR of the two users in the DM
	Text             string
	FileAttachments  []fileAttachment  `gorm:"foreignKey:MessageID"`
	ImageAttachments []imageAttachment `gorm:"foreignKey:MessageID"`
}

func (dm *directMessage) getNonce() nonce {
	return dm.Nonce
}

func (dm *directMessage) generateID() ID {
	ch := newContentHasher(typeDirectMessage, dm.Nonce).
		id(dm.Author).
		id(dm.Xor).
		int64(dm.WrittenAt).
		int64(dm.DeleteAt).
		bytes([]byte(dm.Text))
	packImageAttachments(ch, dm.ImageAttachments)
	packFileAttachments(ch, dm.FileAttachments)
	return ch.generate()
}

func (dm *directMessage) BeforeCreate(tx *gorm.DB) error {
	if !idMatchesContent(dm) {
		return errors.New("direct message ID is not generated from its content")
	}
	dm.SavedAt = time.Now().Unix()
	return nil
}

func (dm *directMessage) AfterDelete(tx *gorm.DB) error {
	if dm.ID == NilID {
		return nil
	}
	err := tx.Clauses(clause.Returning{}).Where("message_id = ?", dm.ID).Delete(&imageAttachment{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("message_id = ?", dm.ID).Delete(&fileAttachment{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", dm.ID, typeDirectMessage).Delete(&deliveryRecord{}).Error
	if err != nil {
		return err
	}
	return tx.Clauses(clause.Returning{}).Where("target = ? AND target_type = ?", dm.ID, typeDirectMessage).Delete(&readReceipt{}).Error
}

func (dm *directMessage) getID() ID {
	return dm.ID
}

func (dm *directMessage) getScope(_ ID) int {
	if dm.Xor == NilID {
		// A DM to ourselves, only needs to be sent to sync devices
		return scopeSync
	}
	return scopeUser
}

func (dm *directMessage) getDestination(myID ID) ID {
	return xor(dm.Xor, myID)
}

func (dm *directMessage) getType() uint16 {
	return typeDirectMessage
}

func (dm *directMessage) getPayload() []byte {
	bytes, err := msgpack.Marshal(signedContainer{
		Payload:   dm.OriginalPayload,
		Signature: dm.Signature,
		Signer:    dm.Signer,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling direct message's signed container")
	}

	return bytes
}

func (dm *directMessage) getAuthor() ID {
	return dm.Author
}

func (dm *directMessage) getTimestamp() int64 {
	return dm.WrittenAt
}

func (dm *directMessage) getSavedAt() int64 {
	return dm.SavedAt
}

func (dm *directMessage) empty() bool {
	return strings.TrimSpace(dm.Text) == "" && len(dm.ImageAttachments) == 0 && len(dm.FileAttachments) == 0
}

func (b *Bounce) handleDirectMessage(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	directMessageMutex.Lock()
	defer directMessageMutex.Unlock()
	readReceiptMutex.Lock()
	defer readReceiptMutex.Unlock()

	// Verify the signature
	sc, err := b.unpackSignedContainer(typeDirectMessage, payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for direct message")
		return nil, false
	}
	var dm directMessage
	err = msgpack.Unmarshal(sc.Payload, &dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling direct message")
		return nil, false
	}
	dm.OriginalPayload = sc.Payload
	dm.Signature = sc.Signature
	dm.Signer = sc.Signer

	// Make sure the ID is generated from the content
	if !idMatchesContent(&dm) {
		log.WithFields(log.Fields{
			"peer":       peer,
			"id":         dm.getID(),
			"content_id": dm.generateID(),
		}).Error("rejecting direct message with an ID not generated from its content")
		return nil, false
	}

	// Make sure the signing device was not revoked before creating this
	var signerDevice device
	err = b.database.Select("revoked_at").Where("address = ?", dm.Signer).First(&signerDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": dm.Signer,
			}).Error("signer device not found for direct message")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"address": dm.Signer,
				"error":   err.Error(),
			}).Fatal("database error looking up signing device")
		}
	}
	if signerDevice.RevokedAt != 0 && signerDevice.RevokedAt < dm.WrittenAt {
		log.WithFields(log.Fields{
			"id":     dm.ID,
			"signer": dm.Signer,
		}).Warn("ignoring direct message signed by revoked device")
		go b.sendAck(peer, typeDirectMessage, dm.ID)
		return nil, false
	}

	// Make sure the device that signed this message belongs to the author
	if !b.signedByUser(sc, dm.Author) {
		log.WithFields(log.Fields{
			"id":     dm.ID,
			"signer": sc.Signer,
			"author": dm.Author,
		}).Warn("received direct message signed by a different user than the author, ignoring")
		return nil, false
	}

	// Make sure the thread and author line up
	if !(dm.Author == b.currentUserID() || xor(dm.Xor, dm.Author) == b.currentUserID()) {
		log.WithFields(log.Fields{
			"id":     dm.ID,
			"author": dm.Author,
		}).Error("reject DM with author that does not line up with xor")
		return nil, false
	}

	// Ignore anything from a blocked user
	if blockedUser(dm.Author) {
		log.WithFields(log.Fields{
			"id":     dm.ID,
			"author": dm.getAuthor(),
		}).Warn("ignoring direct message from blocked user")

		if peerDev, ok := b.getDeviceFromAddress(peer); ok {
			if !blockedUser(peerDev.UserID) {
				go b.sendAck(peer, typeDirectMessage, dm.ID)
			}
		}
		return nil, false
	}

	// Ignore empty messages
	if dm.empty() {
		log.WithFields(log.Fields{
			"id": dm.ID,
		}).Warn("ignoring empty direct message")
		go b.sendAck(peer, typeDirectMessage, dm.ID)
		return nil, false
	}

	// Ignore messages that are too long
	if utf8.RuneCountInString(dm.Text) > MaximumMessageCharacters {
		log.WithFields(log.Fields{
			"id": dm.ID,
		}).Warn("ignoring direct message that is too long")
		go b.sendAck(peer, typeGroupMessage, dm.ID)
		return nil, false
	}

	// If we have already seen this message, all we need to do is mark that this peer has the message as well and ack it
	var existingDM directMessage
	err = b.database.Where("id = ?", dm.ID).First(&existingDM).Error
	if err == nil {
		return &existingDM, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up direct message")
	}

	// If the message is older than the user's ClearBefore, don't process it
	if b.directMessageWrittenBeforeHistoryCleared(dm.getDestination(b.currentUserID()), dm.WrittenAt) {
		log.WithFields(log.Fields{
			"author":      dm.Author,
			"destination": dm.getDestination(b.currentUserID()),
			"written_at":  dm.WrittenAt,
		}).Debug("ignoring a direct message that was written before the history was cleared")
		return nil, false
	}

	// Ignore messages that should have already been deleted
	if dm.DeleteAt != 0 && dm.DeleteAt <= time.Now().Unix() {
		log.WithFields(log.Fields{
			"id":          dm.ID,
			"author":      dm.Author,
			"destination": dm.getDestination(b.currentUserID()),
			"delete_at":   dm.DeleteAt,
		}).Debug("ignoring a direct message that has delete time that occured before now")
		return nil, false
	}

	// If we wrote this message, assume we've seen it
	dm.Seen = dm.Author == b.currentUserID()

	// Save the new message
	err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&dm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving incoming direct message")
	}

	// Update the activity timestamp on the user model
	b.updateLastUserActivity(dm.getDestination(b.currentUserID()), dm.SavedAt)

	// Make sure the user interface isn't still displaying that the user is typing
	b.clearUserTypingIndicator(dm.Author, dm.getDestination(b.currentUserID()), typeDirectMessage)

	// Send the message to the user interface
	if !catchUp {
		uiImageAttachments := []ImageAttachment{}
		for _, ia := range dm.ImageAttachments {
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
		for _, fa := range dm.FileAttachments {
			uiFileAttachments = append(uiFileAttachments, FileAttachment{
				ID:   fa.FileID,
				Name: fa.Name,
				Size: fa.Size,
			})
		}

		b.ui.DisplayDirectMessage(DirectMessage{
			ID:               dm.ID,
			Author:           dm.Author,
			Thread:           dm.getDestination(b.currentUserID()),
			WrittenAt:        dm.WrittenAt,
			SavedAt:          dm.SavedAt,
			ExpiresAt:        dm.DeleteAt,
			Seen:             dm.Seen,
			Text:             dm.Text,
			ImageAttachments: uiImageAttachments,
			FileAttachments:  uiFileAttachments,
		})

		if b.postNotification != nil && b.shouldNotifyDM(&dm) {
			title, content, err := b.getDMNotificationContent(&dm)
			if err == nil {
				b.postNotification(
					dm.ID.String(),
					title,
					content,
					dm.getDestination(b.currentUserID()).String(),
					b.getNotificationIcon(dm.getDestination(b.currentUserID()).String()),
				)
			}
		}

		if owner, ok := b.peerOwner(peer); ok {
			b.ui.MessageDelivered(dm.ID, owner)
		} else {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("direct message received from unknown peer")
		}

		// Find any read receipts for this message that came early, add missing data, broadcast and send to the UI
		b.processEarlyReadReceipts(dm.ID, typeDirectMessage, true)
	}

	return &dm, true
}

func (b *Bounce) shouldNotifyDM(dm *directMessage) bool {
	if dm.Author == b.currentUserID() {
		return false
	}

	var author user
	err := b.database.Take(&author, "id = ?", dm.Author).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting the author of a direct message")
		return false
	}

	if author.MutedUntil == MutedForever {
		return false
	}

	if author.MutedUntil > time.Now().Unix() {
		return false
	}

	if waitingForInitialSyncFrom() != "" {
		return false
	}

	if anotherDeviceIsActive.Load() && !uiIsInForeground.Load() {
		return false
	}

	if uiIsInForeground.Load() && autoscrolling(dm.getDestination(b.currentUserID())) {
		return false
	}

	return true
}

func (b *Bounce) getDMNotificationContent(dm *directMessage) (string, string, error) {
	var author user
	err := b.database.Take(&author, "id = ?", dm.Author).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting the author of a direct message")
		return "", "", err
	}

	content := dm.Text
	if content == "" {
		content = "posted "

		if len(dm.ImageAttachments) > 1 {
			content += "images"
		} else if len(dm.ImageAttachments) == 1 {
			content += "an image"
		}

		if len(dm.FileAttachments) > 1 {
			if len(dm.ImageAttachments) != 0 {
				content += " and "
			}
			content += "files"
		} else if len(dm.FileAttachments) == 1 {
			if len(dm.ImageAttachments) != 0 {
				content += " and "
			}
			content += "a file"
		}
	}

	return author.Name, content, nil
}

var errMessageTooLong = errors.New("message is too long")
var errMessageEmpty = errors.New("message has no text or attachments")
var errRecipientBlocked = errors.New("cannot send a message to a blocked user")
var errAttachmentNotDistributed = errors.New("an attachment could not be distributed")

// SendDirectMessage sends a message to a user.  Attachments that cannot be read or are too large to embed are left
// out and the rest of the message is still sent; an error means nothing was sent.
func (b *Bounce) SendDirectMessage(thread ID, text string, attachments []OutgoingAttachment) error {
	if utf8.RuneCountInString(text) > MaximumMessageCharacters {
		log.Error("refusing to send message that is too long")
		closeAttachments(attachments)
		return errMessageTooLong
	}
	if blockedUser(thread) {
		log.WithFields(log.Fields{
			"user_id": thread,
		}).Warn("refusing to send DM to blocked user")
		closeAttachments(attachments)
		return errRecipientBlocked
	}

	now := time.Now()
	deleteAt := int64(0)
	retentionSeconds := b.getDMRetention(thread)
	if retentionSeconds > 0 {
		deleteAt = now.Unix() + retentionSeconds
	}
	dm := &directMessage{
		Nonce:     newNonce(),
		WrittenAt: now.Unix(),
		Seen:      true,
		Author:    b.currentUserID(),
		Xor:       xor(b.currentUserID(), thread),
		DeleteAt:  deleteAt,
		Text:      text,
	}

	// Attachments are part of the message's content, and publishing one sends a file that names the message, so
	// the attachments are prepared first, the message gets its ID, and only then are the files published
	scope := scopeUser
	destination := xor(thread, b.currentUserID())
	if thread == b.currentUserID() {
		scope = scopeSync
		destination = NilID
	}
	attached := b.prepareMessageAttachments(attachments, scope)
	dm.ImageAttachments = attached.images
	dm.FileAttachments = attached.files

	if dm.empty() {
		log.Error("refusing to send empty direct message")
		return errMessageEmpty
	}

	dm.ID = dm.generateID()
	attached.stampMessageID(dm.ID)
	err := b.publishMessageAttachments(attached, destination, dm.ID)
	if err != nil {
		return err
	}

	dm.OriginalPayload, err = msgpack.Marshal(dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling direct message")
	}
	sc := b.createSignedContainer(typeDirectMessage, dm.OriginalPayload)
	dm.Signature = sc.Signature
	dm.Signer = sc.Signer

	err = b.database.Create(dm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving direct message to the database")
	}
	b.updateLastUserActivity(thread, dm.SavedAt)

	go b.checkIfDirectMessageUndeliverableAt(now.Add(undeliverableAfter).Unix(), dm.ID)

	go b.ui.DisplaySentDirectMessage(DirectMessage{
		ID:               dm.ID,
		Author:           dm.Author,
		Thread:           dm.getDestination(b.currentUserID()),
		WrittenAt:        dm.WrittenAt,
		SavedAt:          dm.SavedAt,
		ExpiresAt:        dm.DeleteAt,
		Text:             dm.Text,
		Seen:             dm.Seen,
		Undeliverable:    dm.Undeliverable,
		ImageAttachments: attached.imageDTOs,
		FileAttachments:  attached.fileDTOs,
	})

	b.broadcast(dm)
	return nil
}

func (b *Bounce) deleteDirectMessageAt(timestamp int64, id ID) {
	// Sleep as long as needed
	duration := timestamp - time.Now().Unix()
	if duration > 0 {
		time.Sleep(time.Duration(duration) * time.Second)
	}

	// Delete from the database
	err := b.database.Clauses(clause.Returning{}).Where("id = ?", id).Delete(&directMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"message_id": id,
			"error":      err.Error(),
		}).Fatal("error deleting direct message that expired")
	}

	// Delete from the UI
	b.ui.DeleteItem(id)
}

func (b *Bounce) checkIfDirectMessageUndeliverableAt(timestamp int64, id ID) {
	// Create a receiver for delivery notifications
	delivered := make(chan bool, 1)
	dmDeliveryNotificationMutex.Lock()
	dmDeliveryNotifications[id] = delivered
	dmDeliveryNotificationMutex.Unlock()

	// Create a timer that waits until the timestamp
	duration := time.Duration(timestamp-time.Now().Unix()) * time.Second
	sleeper := time.NewTimer(duration)
	defer sleeper.Stop()

	// If this message gets ack'd then just return here, otherwise wait until the timestamp
	select {
	case <-delivered:
		dmDeliveryNotificationMutex.Lock()
		delete(dmDeliveryNotifications, id)
		dmDeliveryNotificationMutex.Unlock()
		return
	case <-sleeper.C:
		dmDeliveryNotificationMutex.Lock()
		delete(dmDeliveryNotifications, id)
		dmDeliveryNotificationMutex.Unlock()
		break
	}

	// Check if the message still hasn't been delivered
	var count int64
	err := b.database.Model(&deliveryRecord{}).Where("frame_id = ? AND frame_type = ?", id, typeDirectMessage).Count(&count).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error counting delivery records for direct message")
	}

	// If it hasn't been delivered, mark it as undeliverable
	if count == 0 {
		// Find the DM
		var dm directMessage
		err = b.database.Where("id = ?", id).First(&dm).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"message_id": id,
				}).Warn("no direct message found when checking for delivery")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error looking up direct message")
			}
		}

		// This message is undeliverable, update the undeliverable field and inform the UI
		err = b.database.Model(&dm).Update("undeliverable", true).Error
		if err != nil {
			log.WithFields(log.Fields{
				"message_id": dm.ID,
				"error":      err.Error(),
			}).Fatal("error updating undeliverable field of undeliverable direct message")
		}
		b.ui.MarkMessageUndeliverable(dm.ID)
	}
}
