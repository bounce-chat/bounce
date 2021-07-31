package ui

import (
	"sync"
)

type userStore struct {
	sync.Mutex
	userMap  map[string]*user
	userList []*user
}

func newUserStore() *userStore {
	return &userStore{
		userMap:  make(map[string]*user),
		userList: []*user{},
	}
}

func (store *userStore) add(u *user) {
	store.Lock()
	defer store.Unlock()

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

func (store *userStore) remove(uuid string) {
	store.Lock()
	defer store.Unlock()

	delete(store.userMap, uuid)
	newList := []*user{}
	for _, u := range store.userList {
		if u.id != uuid {
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

func (store *userStore) get(uuid string) (*user, bool) {
	store.Lock()
	defer store.Unlock()

	u, exists := store.userMap[uuid]
	return u, exists
}

func (store *userStore) empty() {
	store.Lock()
	defer store.Unlock()

	store.userMap = make(map[string]*user)
	store.userList = []*user{}
}
