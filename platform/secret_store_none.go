//go:build android || ios || !(linux || freebsd || openbsd || netbsd || dragonfly || windows || darwin)

package platform

import "github.com/bounce-chat/bounce/chat"

// newSecretStore returns no store on a platform this package has none for.  Android is here because its keystore is
// reached from Kotlin: the Android client builds its own Platform around a store Kotlin implements.
func newSecretStore(configDirectory string) chat.SecretStore {
	return nil
}
