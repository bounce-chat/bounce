package chat

import (
	"testing"
)

// These tests exist to convert the failure mode the registry was built to kill
// - a frame type wired into some stages of the pipeline but not all of them,
// which works live and silently never syncs - into a build failure.
//
// If you are here because one of them broke after adding a frame type, the fix
// is almost always to complete the entry in referencedFrames rather than to relax the
// assertion.

func TestEverySpecIsComplete(t *testing.T) {
	seen := map[uint16]bool{}

	for _, spec := range referencedFrames {
		if seen[spec.frameType] {
			t.Errorf("frame type %d appears twice in referencedFrames", spec.frameType)
		}
		seen[spec.frameType] = true

		if spec.table == "" {
			t.Errorf("frame type %d has no table", spec.frameType)
		}

		if spec.load == nil {
			t.Errorf("syncable frame type %d has no load function, so a peer "+
				"requesting it would be answered with nothing", spec.frameType)
		}
		if spec.offer == nil {
			t.Errorf("syncable frame type %d has no offer function, so it would "+
				"never be offered to an offline peer", spec.frameType)
		}
	}
}

// Every syncable type must be dispatchable, or a catch up containing it is
// accepted by allowedCatchUpFrames and then dropped for want of a handler.
func TestEverySyncableTypeHasAHandler(t *testing.T) {
	b := &Bounce{}
	handlers := b.getHandlers(false)

	for _, spec := range referencedFrames {
		if _, ok := handlers[spec.frameType]; !ok {
			t.Errorf("syncable frame type %d has no entry in the normal handler map", spec.frameType)
		}
	}
}

// The derived tables are what the rest of the package reads, so check they
// actually came out of the registry rather than being left empty by an init
// ordering mistake.
func TestDerivedTablesMatchTheRegistry(t *testing.T) {
	if len(typeTable) != len(referencedFrames) {
		t.Errorf("typeTable has %d entries, referencedFrames has %d", len(typeTable), len(referencedFrames))
	}

	if len(catchUpOrder) != len(referencedFrames) {
		t.Errorf("catchUpOrder has %d entries, %d types are syncable", len(catchUpOrder), len(referencedFrames))
	}

	if len(allowedCatchUpFrames) != len(referencedFrames) {
		t.Errorf("allowedCatchUpFrames has %d entries, %d types are syncable",
			len(allowedCatchUpFrames), len(referencedFrames))
	}

	if len(referencedTypes) != len(referencedFrames) {
		t.Errorf("referencedTypes has %d entries, referencedFrames has %d",
			len(referencedTypes), len(referencedFrames))
	}

	// catchUpOrder ranks must be a dense 0..n-1, since Less compares them
	// directly and a gap would mean a type was skipped while building them.
	ranks := map[int]bool{}
	for frameType, rank := range catchUpOrder {
		if rank < 0 || rank >= len(referencedFrames) {
			t.Errorf("frame type %d has catch up rank %d, outside 0..%d",
				frameType, rank, len(referencedFrames)-1)
		}
		if ranks[rank] {
			t.Errorf("catch up rank %d is used by more than one frame type", rank)
		}
		ranks[rank] = true
	}

	for _, spec := range referencedFrames {
		if typeTable[spec.frameType] != spec.table {
			t.Errorf("typeTable[%d] is %q, registry says %q",
				spec.frameType, typeTable[spec.frameType], spec.table)
		}
		if !allowedCatchUpFrames[spec.frameType] {
			t.Errorf("syncable frame type %d is missing from allowedCatchUpFrames", spec.frameType)
		}
	}
}

// The order of referencedFrames is the replay order of a catch up, and some of it
// is a real ordering constraint rather than a preference: a group cannot be
// updated before it has been created, and frames authored by a device cannot be
// verified before that device is known.
func TestCatchUpOrderRespectsDependencies(t *testing.T) {
	mustPrecede := []struct{ first, second uint16 }{
		{typeAddUser, typeDevice},
		{typeDevice, typeDirectMessage},
		{typeDevice, typeGroupMessage},
		{typeGroupCreation, typeUpdateGroup},
		{typeGroupCreation, typeGroupMessage},
		{typeUpdateGroup, typeConfirmation},
		{typeGroupMessage, typeReadReceipt},
		{typeFile, typeChunkOffer},
	}

	for _, pair := range mustPrecede {
		first, ok := catchUpOrder[pair.first]
		if !ok {
			t.Fatalf("frame type %d is not in catchUpOrder", pair.first)
		}
		second, ok := catchUpOrder[pair.second]
		if !ok {
			t.Fatalf("frame type %d is not in catchUpOrder", pair.second)
		}

		if first >= second {
			t.Errorf("frame type %d must be replayed before %d, but has rank %d against %d",
				pair.first, pair.second, first, second)
		}
	}
}
