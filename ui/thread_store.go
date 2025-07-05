package ui

import (
	"sync"

	"github.com/google/uuid"
)

type threadStore struct {
	sync.Mutex
	threads map[uuid.UUID]thread
}

func newThreadStore() *threadStore {
	return &threadStore{
		threads: map[uuid.UUID]thread{},
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

	delete(ts.threads, id)
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

func (ts *threadStore) rangeFunc(f func(id uuid.UUID, t thread)) {
	ts.Lock()
	defer ts.Unlock()

	for id, t := range ts.threads {
		f(id, t)
	}
}
