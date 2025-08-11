package ui

import (
	"sort"
	"sync"

	"github.com/google/uuid"
)

type threadStore struct {
	sync.Mutex
	threads map[uuid.UUID]thread // thread ID to thread
	items   map[uuid.UUID]thread // thread item ID to thread
}

func newThreadStore() *threadStore {
	return &threadStore{
		threads: map[uuid.UUID]thread{},
		items:   map[uuid.UUID]thread{},
	}
}

func (ts *threadStore) add(id uuid.UUID, t thread) {
	ts.Lock()
	defer ts.Unlock()

	ts.threads[id] = t
}

func (ts *threadStore) get(id uuid.UUID) (thread, bool) {
	ts.Lock()
	defer ts.Unlock()

	t, ok := ts.threads[id]
	return t, ok
}

func (ts *threadStore) remove(id uuid.UUID) {
	ts.Lock()
	defer ts.Unlock()

	// Prune the items map of items that point to this thread
	for itemID, t := range ts.items {
		if t.getID() == id {
			delete(ts.items, itemID)
		}
	}

	delete(ts.threads, id)
}

func (ts *threadStore) associate(t thread, itemID uuid.UUID) {
	ts.Lock()
	defer ts.Unlock()

	ts.items[itemID] = t
}

func (ts *threadStore) withItem(id uuid.UUID) (thread, bool) {
	ts.Lock()
	defer ts.Unlock()

	t, ok := ts.items[id]
	return t, ok
}

func (ts *threadStore) removeItem(id uuid.UUID) {
	ts.Lock()
	defer ts.Unlock()

	delete(ts.items, id)
}

func (ts *threadStore) getGroup(id uuid.UUID) (*group, bool) {
	ts.Lock()
	defer ts.Unlock()

	t, ok := ts.threads[id]
	if !ok {
		return nil, false
	}

	g, ok := t.(*group)
	if !ok {
		return nil, false
	}

	return g, true
}

func (ts *threadStore) getDM(id uuid.UUID) (*directMessage, bool) {
	ts.Lock()
	defer ts.Unlock()

	t, ok := ts.threads[id]
	if !ok {
		return nil, false
	}

	dm, ok := t.(*directMessage)
	if !ok {
		return nil, false
	}

	return dm, true
}

func (ts *threadStore) rangeFunc(f func(t thread)) {
	threads := []thread{}
	ts.Lock()
	for _, t := range ts.threads {
		threads = append(threads, t)
	}
	ts.Unlock()

	for _, t := range threads {
		f(t)
	}
}

func (ts *threadStore) sorted() []thread {
	threads := sortableThreads{}
	ts.Lock()
	for _, t := range ts.threads {
		threads = append(threads, t)
	}
	ts.Unlock()

	sort.Sort(threads)
	return threads
}
