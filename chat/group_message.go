package chat

import (
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var gmDeliveryNotificationMutex sync.Mutex
var gmDeliveryNotifications = map[uuid.UUID]chan bool{}

// A group message is sent from a member of a group to a group
type groupMessage struct {
	SignedFrame
	cachedEncoding
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt          int64     `msgpack:"-"`
	WrittenAt        int64
	DeleteAt         int64
	Seen             bool `msgpack:"-"`
	Undeliverable    bool `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Author           uuid.UUID
	Destination      uuid.UUID
	Text             string
	FileAttachments  []fileAttachment  `gorm:"foreignKey:MessageID"`
	ImageAttachments []imageAttachment `gorm:"foreignKey:MessageID"`
}

func (gm *groupMessage) BeforeCreate(tx *gorm.DB) error {
	if gm.ID == uuid.Nil {
		return errors.New("group message must have an ID assigned before creation")
	}
	gm.SavedAt = time.Now().Unix()
	return nil
}

func (gm *groupMessage) AfterDelete(tx *gorm.DB) error {
	if gm.ID == uuid.Nil {
		return nil
	}
	err := tx.Clauses(clause.Returning{}).Where("message_id = ?", gm.ID).Delete(&imageAttachment{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("message_id = ?", gm.ID).Delete(&fileAttachment{}).Error
	if err != nil {
		return err
	}
	err = tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", gm.ID, typeGroupMessage).Delete(&deliveryRecord{}).Error
	if err != nil {
		return err
	}
	return tx.Clauses(clause.Returning{}).Where("target = ? AND target_type = ?", gm.ID, typeGroupMessage).Delete(&readReceipt{}).Error
}

func (gm *groupMessage) getID() uuid.UUID {
	return gm.ID
}

func (gm *groupMessage) getScope(_ uuid.UUID) int {
	return scopeGroup
}

func (gm *groupMessage) getDestination(_ uuid.UUID) uuid.UUID {
	return gm.Destination
}

func (gm *groupMessage) getType() uint16 {
	return typeGroupMessage
}

func (gm *groupMessage) getPayload() []byte {
	if len(gm.payload) == 0 {
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
		gm.payload = bytes
	}

	return gm.payload
}

func (gm *groupMessage) getAuthor() uuid.UUID {
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
	sc, err := b.unpackSignedContainer(payload)
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

		encryptedDeviceCacheMutex.Lock()
		userID, ok := encryptedDeviceCache[peer]
		encryptedDeviceCacheMutex.Unlock()
		if ok {
			b.ui.MessageDelivered(gm.ID, userID)
		} else {
			dev, ok := b.getDeviceFromAddress(peer)
			if ok {
				b.ui.MessageDelivered(gm.ID, dev.UserID)
			} else {
				log.WithFields(log.Fields{
					"peer": peer,
				}).Warn("direct message received from unknown peer")
			}
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

	if waitingForInitialSyncFrom != "" {
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

func (b *Bounce) SendGroupMessage(message GroupMessage, readers map[uuid.UUID]io.ReadCloser, sources map[uuid.UUID]string) {
	if utf8.RuneCountInString(message.Text) > MaximumMessageCharacters {
		log.Error("refusing to send message that is too long")
	}
	if message.ID != uuid.Nil {
		log.Fatal("group message ID cannot be set by the UI")
	}

	gs, err := b.currentGroupState(message.Thread)
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": message.Thread,
			"error":    err.Error(),
		}).Error("error getting group state while sending group message")
		for _, r := range readers {
			r.Close()
		}
		return
	}

	now := time.Now()
	deleteAt := int64(0)
	retentionSeconds := gs.retention
	if retentionSeconds > 0 {
		deleteAt = now.Unix() + retentionSeconds
	}
	gm := &groupMessage{
		ID:          uuid.New(),
		WrittenAt:   now.Unix(),
		Seen:        true,
		Author:      b.currentUserID(),
		Destination: message.Thread,
		DeleteAt:    deleteAt,
		Text:        message.Text,
	}

	includedImageAttachments := []ImageAttachment{}
	for _, ia := range message.ImageAttachments {
		reader, ok := readers[ia.ID]
		if !ok {
			log.WithFields(log.Fields{
				"id":   ia.ID,
				"name": ia.Name,
			}).Error("cannot attach image to message without reader")
			continue
		}

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

		err = b.embedFile(ia.ID, data, scopeGroup, message.Thread, fileTypeMessageAttachment, gm.ID)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    ia.ID,
				"name":  ia.Name,
				"error": err.Error(),
			}).Error("error distributing image attachment")
		} else {
			includedImageAttachments = append(includedImageAttachments, ia)
			gm.ImageAttachments = append(gm.ImageAttachments, imageAttachment{
				ID:        uuid.New(),
				FileID:    ia.ID,
				MessageID: gm.ID,
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
		reader, ok := readers[fa.ID]
		if !ok {
			log.WithFields(log.Fields{
				"id":   fa.ID,
				"name": fa.Name,
			}).Error("cannot attach file to message without reader")
			continue
		}

		if fa.Size > EmbeddedFileLimit {
			reader.Close()
			source, ok := sources[fa.ID]
			if !ok {
				log.WithFields(log.Fields{
					"id":   fa.ID,
					"name": fa.Name,
				}).Error("cannot attach large file to message without reader")
				continue
			}

			go func() {
				err = b.seedFile(fa.ID, source, scopeGroup, message.Thread, fileTypeMessageAttachment, gm.ID)
				if err != nil {
					log.WithFields(log.Fields{
						"id":    fa.ID,
						"name":  fa.Name,
						"error": err.Error(),
					}).Error("error seeding file attachment")
				}
			}()
		} else {
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

			err = b.embedFile(fa.ID, data, scopeGroup, message.Thread, fileTypeMessageAttachment, gm.ID)
			if err != nil {
				log.WithFields(log.Fields{
					"id":    fa.ID,
					"name":  fa.Name,
					"error": err.Error(),
				}).Error("error embedding file attachment")
				continue
			}
		}
		includedFileAttachments = append(includedFileAttachments, fa)
		gm.FileAttachments = append(gm.FileAttachments, fileAttachment{
			ID:        uuid.New(),
			FileID:    fa.ID,
			MessageID: gm.ID,
			Name:      fa.Name,
			Size:      fa.Size,
		})
	}

	if gm.empty() {
		log.Error("refusing to send empty group message")
		return
	}

	gm.OriginalPayload, err = msgpack.Marshal(gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group message")
	}
	sc := b.createSignedContainer(gm.OriginalPayload)
	gm.Signature = sc.Signature
	gm.Signer = sc.Signer

	err = b.database.Create(gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message")
	}
	b.updateLastGroupActivity(gm.Destination, gm.SavedAt)

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
		ImageAttachments: includedImageAttachments,
		FileAttachments:  includedFileAttachments,
	})

	b.broadcast(gm)
}

func (b *Bounce) deleteGroupMessageAt(timestamp int64, id uuid.UUID) {
	// Sleep as long as needed
	duration := timestamp - time.Now().Unix()
	if duration > 0 {
		time.Sleep(time.Duration(duration) * time.Second)
	}

	// Delete from the database
	err := b.database.Clauses(clause.Returning{}).Where("id = ?", id).Delete(&groupMessage{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"message_id": id,
			"error":      err.Error(),
		}).Fatal("error deleting group message that expired")
	}

	// Delete from the UI
	b.ui.DeleteItem(id)
}

func (b *Bounce) checkIfGroupMessageUndeliverableAt(timestamp int64, id uuid.UUID) {
	// Create a receiver for delivery notifications
	delivered := make(chan bool)
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
