package platform

import (
	"errors"
	"os"
	"unsafe"

	"github.com/bounce-chat/bounce/chat"
	"golang.org/x/sys/windows"
)

// A dpapiStore keeps the secret with the Windows Data Protection API.  DPAPI stores nothing itself: it encrypts with a
// key that belongs to the Windows user and hands the ciphertext back, so the ciphertext is kept in a file of ours
// beside the database.  The file is useless to any other user and on any other machine.
type dpapiStore struct {
	file string
}

func newSecretStore(configDirectory string) chat.SecretStore {
	return &dpapiStore{file: configDirectory + "/keystore.bin"}
}

func dataBlob(data []byte) *windows.DataBlob {
	return &windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

// takeDataBlob copies the bytes DPAPI allocated for a result and releases its allocation
func takeDataBlob(blob windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	return append([]byte{}, unsafe.Slice(blob.Data, blob.Size)...)
}

func (s *dpapiStore) Get() ([]byte, error) {
	protected, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, chat.ErrSecretNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(protected) == 0 {
		return nil, chat.ErrSecretNotFound
	}

	// A file this Windows user cannot unprotect was written by another user or on another machine, which is what a
	// copied data folder or a reinstalled Windows looks like.  Trying again will not change that, so it is reported
	// as no entry rather than as a keystore that could not be reached.
	var secret windows.DataBlob
	err = windows.CryptUnprotectData(dataBlob(protected), nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &secret)
	if err != nil {
		return nil, chat.ErrSecretNotFound
	}
	return takeDataBlob(secret), nil
}

func (s *dpapiStore) Store(secret []byte) error {
	if len(secret) == 0 {
		return errors.New("refusing to store an empty secret")
	}

	var protected windows.DataBlob
	err := windows.CryptProtectData(dataBlob(secret), nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &protected)
	if err != nil {
		return err
	}

	// Written beside the final name and renamed over it, so a crash leaves the old file or the new one
	temporaryFile := s.file + ".tmp"
	file, err := os.OpenFile(temporaryFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(takeDataBlob(protected))
	if err != nil {
		file.Close()
		os.Remove(temporaryFile)
		return err
	}
	err = file.Sync()
	if err != nil {
		file.Close()
		os.Remove(temporaryFile)
		return err
	}
	err = file.Close()
	if err != nil {
		os.Remove(temporaryFile)
		return err
	}
	return os.Rename(temporaryFile, s.file)
}
