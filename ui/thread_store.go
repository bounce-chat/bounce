package ui

import (
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type threadStore struct {
	sync.Mutex
	threads map[uuid.UUID]thread // thread ID to thread
	items   map[uuid.UUID]thread // thread item ID to thread
	ngrams  map[string][]thread
}

func newThreadStore() *threadStore {
	return &threadStore{
		threads: map[uuid.UUID]thread{},
		items:   map[uuid.UUID]thread{},
		ngrams:  make(map[string][]thread),
	}
}

func (ts *threadStore) add(id uuid.UUID, t thread) {
	ts.Lock()
	defer ts.Unlock()

	ts.threads[id] = t

	grams := makeNgrams(strings.ToLower(t.getName()))
	for _, gram := range grams {
		ts.ngrams[gram] = append(ts.ngrams[gram], t)
	}
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

	prunedNgrams := map[string][]thread{}
	for gram, threads := range ts.ngrams {
		threadsWithoutThisThread := []thread{}
		for _, t := range threads {
			if t.getID() != id {
				threadsWithoutThisThread = append(threadsWithoutThisThread, t)
			}
		}
		prunedNgrams[gram] = threadsWithoutThisThread
	}
	ts.ngrams = prunedNgrams
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

func (ts *threadStore) groupsWithUser(id uuid.UUID) int {
	ts.Lock()
	defer ts.Unlock()

	count := 0
	for _, t := range ts.threads {
		g, ok := t.(*group)
		if ok {
			if g.users.contains(id) {
				count += 1
			}
		}
	}

	return count
}

func (ts *threadStore) search(str string) []thread {
	if str == "" {
		return ts.sorted()
	}

	ts.Lock()
	defer ts.Unlock()
	results, ok := ts.ngrams[strings.ToLower(str)]
	if !ok {
		return []thread{}
	}

	threads := sortableThreads{}
	for _, r := range results {
		threads = append(threads, r)
	}
	sort.Sort(threads)
	return threads
}
