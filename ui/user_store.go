package ui

import (
	"strings"
	"sync"

	"fyne.io/fyne/v2/data/binding"
	"github.com/google/uuid"
)

type userStore struct {
	sync.Mutex
	userMap  map[uuid.UUID]*user
	userList []*user
	ngrams   map[string][]*user
}

func newUserStore() *userStore {
	return &userStore{
		userMap:  make(map[uuid.UUID]*user),
		userList: []*user{},
		ngrams:   make(map[string][]*user),
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
		if existingUser.getName() < u.getName() {
			smaller++
		}
	}
	smaller_users := store.userList[:smaller]
	larger_users := store.userList[smaller:]
	store.userList = append(smaller_users, append([]*user{u}, larger_users...)...)

	grams := makeNgrams(strings.ToLower(u.getName()))
	for _, gram := range grams {
		store.ngrams[gram] = append(store.ngrams[gram], u)
	}

	// When a use changes their name, remove and re-add them in order to
	// re-sort the list and re-generate ngrams
	u.name.AddListener(binding.NewDataListener(func() {
		store.remove(u.id)
		store.add(u)
	}))
}

func (store *userStore) contains(userID uuid.UUID) bool {
	store.Lock()
	defer store.Unlock()

	_, ok := store.userMap[userID]
	return ok
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

	prunedNgrams := map[string][]*user{}
	for gram, users := range store.ngrams {
		usersWithoutThisUser := []*user{}
		for _, u := range users {
			if u.id != id {
				usersWithoutThisUser = append(usersWithoutThisUser, u)
			}
		}
		prunedNgrams[gram] = usersWithoutThisUser
	}
	store.ngrams = prunedNgrams
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

func (store *userStore) search(str string) []*user {
	store.Lock()
	defer store.Unlock()

	if str == "" {
		return store.userList
	}
	results, ok := store.ngrams[strings.ToLower(str)]
	if !ok {
		return []*user{}
	}
	return results
}

func makeNgrams(str string) []string {
	strLen := len(str)
	ngrams := []string{}
	alreadyPresent := map[string]bool{}

	for i := 1; i <= strLen; i++ {
		for n := 0; n+i <= strLen; n++ {
			gram := str[n : n+i]
			if _, present := alreadyPresent[gram]; !present {
				ngrams = append(ngrams, gram)
				alreadyPresent[gram] = true
			}
		}
	}

	return ngrams
}
