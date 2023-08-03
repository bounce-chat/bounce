package ui

import (
	"sync"

	"github.com/google/uuid"
)

type userStore struct {
	sync.Mutex
	userMap  map[uuid.UUID]*user
	userList []*user
}

func newUserStore() *userStore {
	return &userStore{
		userMap:  make(map[uuid.UUID]*user),
		userList: []*user{},
	}
}

func (store *userStore) add(u *user) {
	store.Lock()
	defer store.Unlock()

	_, exists := store.userMap[u.id]
	if exists {
		return
	}

	store.userMap[u.id] = u
	smaller := 0
	for _, existingUser := range store.userList {
		if existingUser.name < u.name {
			smaller++
		}
	}
	smaller_users := store.userList[:smaller]
	larger_users := store.userList[smaller:]
	store.userList = append(smaller_users, append([]*user{u}, larger_users...)...)
}

func (store *userStore) remove(id uuid.UUID) {
	store.Lock()
	defer store.Unlock()

	delete(store.userMap, id)
	newList := []*user{}
	for _, u := range store.userList {
		if u.id != id {
			newList = append(newList, u)
		}
	}
	store.userList = newList
}

func (store *userStore) alphabetized() []*user {
	store.Lock()
	defer store.Unlock()

	return store.userList
}

func (store *userStore) get(id uuid.UUID) (*user, bool) {
	store.Lock()
	defer store.Unlock()

	u, exists := store.userMap[id]
	return u, exists
}

func (store *userStore) empty() {
	store.Lock()
	defer store.Unlock()

	store.userMap = make(map[uuid.UUID]*user)
	store.userList = []*user{}
}
