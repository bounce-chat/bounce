package chat

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var directMessageMutex sync.Mutex

var dmDeliveryNotificationMutex sync.Mutex
var dmDeliveryNotifications = map[uuid.UUID]chan bool{}

// A direct message is a chat message from one user to another
type directMessage struct {
	SignedFrame
	cachedEncoding
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt          int64     `msgpack:"-"`
	WrittenAt        int64
	DeleteAt         int64
	Seen             bool `msgpack:"-"`
	Undeliverable    bool `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Author           uuid.UUID
	Xor              uuid.UUID // XOR of the two users in the DM
	Text             string
	FileAttachments  []fileAttachment  `gorm:"foreignKey:MessageID"`
	ImageAttachments []imageAttachment `gorm:"foreignKey:MessageID"`
}

func (dm *directMessage) BeforeCreate(tx *gorm.DB) error {
	if dm.ID == uuid.Nil {
		return errors.New("direct message must have an ID assigned before creation")
	}
	dm.SavedAt = time.Now().Unix()
	return nil
}

func (dm *directMessage) AfterDelete(tx *gorm.DB) error {
	if dm.ID == uuid.Nil {
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

func (dm *directMessage) getID() uuid.UUID {
	return dm.ID
}

func (dm *directMessage) getScope(_ uuid.UUID) int {
	if dm.Xor == uuid.Nil {
		// A DM to ourselves, only needs to be sent to sync devices
		return scopeSync
	}
	return scopeUser
}

func (dm *directMessage) getDestination(myID uuid.UUID) uuid.UUID {
	return xor(dm.Xor, myID)
}

func (dm *directMessage) getType() uint16 {
	return typeDirectMessage
}

func (dm *directMessage) getPayload() []byte {
	if len(dm.payload) == 0 {
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
		dm.payload = bytes
	}
	return dm.payload
}

func (dm *directMessage) getAuthor() uuid.UUID {
	return dm.Author
}

func (dm *directMessage) getTimestamp() int64 {
	return dm.WrittenAt
}

func (dm *directMessage) empty() bool {
	return dm.Text == "" && len(dm.ImageAttachments) == 0 && len(dm.FileAttachments) == 0
}

func (b *Bounce) handleDirectMessage(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	directMessageMutex.Lock()
	defer directMessageMutex.Unlock()
	readReceiptMutex.Lock()
	defer readReceiptMutex.Unlock()

	// Verify the signature
	sc, err := b.unpackSignedContainer(payload)
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

		encryptedDeviceCacheMutex.Lock()
		userID, ok := encryptedDeviceCache[peer]
		encryptedDeviceCacheMutex.Unlock()
		if ok {
			b.ui.MessageDelivered(dm.ID, userID)
		} else {
			dev, ok := b.getDeviceFromAddress(peer)
			if ok {
				b.ui.MessageDelivered(dm.ID, dev.UserID)
			} else {
				log.WithFields(log.Fields{
					"peer": peer,
				}).Warn("direct message received from unknown peer")
			}
		}

		// Find any read receipts for this message that came early, add missing data, broadcast and send to the UI
		b.processEarlyReadReceipts(dm.ID, typeDirectMessage, true)
	}

	return &dm, true
}

func (b *Bounce) SendDirectMessage(message DirectMessage, readers map[uuid.UUID]io.ReadCloser, sources map[uuid.UUID]string) {
	if message.ID != uuid.Nil {
		log.Fatal("direct message ID cannot be set by the UI")
	}

	if blockedUser(message.Thread) {
		log.WithFields(log.Fields{
			"user_id": message.Thread,
		}).Warn("refusing to send DM to blocked user")
		return
	}

	now := time.Now()
	deleteAt := int64(0)
	retentionSeconds := b.getDMRetention(message.Thread)
	if retentionSeconds > 0 {
		deleteAt = now.Unix() + retentionSeconds
	}
	dm := &directMessage{
		ID:        uuid.New(),
		WrittenAt: now.Unix(),
		Seen:      true,
		Author:    b.currentUserID(),
		Xor:       xor(b.currentUserID(), message.Thread),
		DeleteAt:  deleteAt,
		Text:      message.Text,
	}
	message.ID = dm.ID
	message.Author = b.currentUserID()
	message.WrittenAt = now.Unix()

	includedImageAttachments := []ImageAttachment{}
	for _, ia := range message.ImageAttachments {
		if ia.Size > EmbeddedFileLimit {
			message.FileAttachments = append(
				message.FileAttachments,
				FileAttachment{
					ID:   ia.ID,
					Name: ia.Name,
					Size: ia.Size,
				},
			)
			continue
		}

		reader, ok := readers[ia.ID]
		if !ok {
			log.WithFields(log.Fields{
				"id":   ia.ID,
				"name": ia.Name,
			}).Error("cannot attach image to message without reader")
			continue
		}

		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"id":    ia.ID,
				"name":  ia.Name,
				"error": err.Error(),
			}).Error("error reading image data to attach to message")
			continue
		}

		scope := scopeUser
		destination := xor(message.Thread, b.currentUserID())
		if message.Thread == b.currentUserID() {
			scope = scopeSync
			destination = b.currentUserID()
		}
		err = b.embedFile(ia.ID, data, scope, destination, fileTypeMessageAttachment, dm.ID)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    ia.ID,
				"name":  ia.Name,
				"error": err.Error(),
			}).Error("error distributing image attachment")
		} else {
			includedImageAttachments = append(includedImageAttachments, ia)
			dm.ImageAttachments = append(dm.ImageAttachments, imageAttachment{
				ID:        uuid.New(),
				FileID:    ia.ID,
				MessageID: dm.ID,
				Name:      ia.Name,
				Size:      ia.Size,
				Width:     ia.Width,
				Height:    ia.Height,
				BlurHash:  ia.BlurHash,
			})
		}
	}
	includedFileAttachments := []FileAttachment{}
	for _, fa := range message.FileAttachments {
		if fa.Size > EmbeddedFileLimit {
			source, ok := sources[fa.ID]
			if !ok {
				log.WithFields(log.Fields{
					"id":   fa.ID,
					"name": fa.Name,
				}).Error("cannot attach large large file to message without source")
				continue
			}

			scope := scopeUser
			destination := xor(message.Thread, b.currentUserID())
			if message.Thread == b.currentUserID() {
				scope = scopeSync
				destination = b.currentUserID()
			}
			err := b.seedFile(fa.ID, source, scope, destination, fileTypeMessageAttachment, dm.ID)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    fa.ID,
					"name":  fa.Name,
					"error": err.Error(),
				}).Error("error seeding file attachment")
				continue
			}
		} else {
			reader, ok := readers[fa.ID]
			if !ok {
				log.WithFields(log.Fields{
					"id":   fa.ID,
					"name": fa.Name,
				}).Error("cannot attach file to message without reader")
				continue
			}

			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				log.WithFields(log.Fields{
					"id":    fa.ID,
					"name":  fa.Name,
					"error": err.Error(),
				}).Error("error reading file data to attach to message")
				continue
			}

			scope := scopeUser
			destination := xor(message.Thread, b.currentUserID())
			if message.Thread == b.currentUserID() {
				scope = scopeSync
				destination = b.currentUserID()
			}
			err = b.embedFile(fa.ID, data, scope, destination, fileTypeMessageAttachment, dm.ID)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    fa.ID,
					"name":  fa.Name,
					"error": err.Error(),
				}).Error("error distributing file attachment")
			}
		}
		includedFileAttachments = append(includedFileAttachments, fa)
		dm.FileAttachments = append(dm.FileAttachments, fileAttachment{
			ID:        uuid.New(),
			FileID:    fa.ID,
			MessageID: dm.ID,
			Name:      fa.Name,
			Size:      fa.Size,
		})
	}

	if dm.empty() {
		log.Error("refusing to send empty direct message")
		return
	}

	var err error
	dm.OriginalPayload, err = msgpack.Marshal(dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling direct message")
	}
	sc := b.createSignedContainer(dm.OriginalPayload)
	dm.Signature = sc.Signature
	dm.Signer = sc.Signer

	err = b.database.Create(dm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving direct message to the database")
	}
	b.updateLastUserActivity(message.Thread, dm.SavedAt)

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
		ImageAttachments: includedImageAttachments,
		FileAttachments:  includedFileAttachments,
	})

	b.broadcast(dm)
}

func (b *Bounce) deleteDirectMessageAt(timestamp int64, id uuid.UUID) {
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

func (b *Bounce) checkIfDirectMessageUndeliverableAt(timestamp int64, id uuid.UUID) {
	// Create a receiver for delivery notifications
	delivered := make(chan bool)
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
