package chat

import (
	"testing"
)

// These tests exist to convert the failure mode the registry was built to kill
// - a frame type wired into some stages of the pipeline but not all of them,
// which works live and silently never syncs - into a build failure.
//
// If you are here because one of them broke after adding a frame type, the fix
// is almost always to complete the entry in frameSpecs rather than to relax the
// assertion.

func TestEverySpecIsComplete(t *testing.T) {
	seen := map[uint16]bool{}

	for _, spec := range frameSpecs {
		if seen[spec.typ] {
			t.Errorf("frame type %d appears twice in frameSpecs", spec.typ)
		}
		seen[spec.typ] = true

		if spec.table == "" {
			t.Errorf("frame type %d has no table", spec.typ)
		}

		if spec.syncable {
			if spec.load == nil {
				t.Errorf("syncable frame type %d has no load function, so a peer "+
					"requesting it would be answered with nothing", spec.typ)
			}
			if spec.offer == nil {
				t.Errorf("syncable frame type %d has no offer function, so it would "+
					"never be offered to an offline peer", spec.typ)
			}
			continue
		}

		if spec.load != nil || spec.offer != nil || spec.dialWorthy {
			t.Errorf("frame type %d is not syncable but carries reference-flow "+
				"fields; either mark it syncable or drop them", spec.typ)
		}
	}
}

// Every syncable type must be dispatchable, or a catch up containing it is
// accepted by allowedCatchUpFrames and then dropped for want of a handler.
func TestEverySyncableTypeHasAHandler(t *testing.T) {
	b := &Bounce{}
	handlers := b.getHandlers(false)

	for _, spec := range syncableSpecs {
		if _, ok := handlers[spec.typ]; !ok {
			t.Errorf("syncable frame type %d has no entry in the normal handler map", spec.typ)
		}
	}
}

// The derived tables are what the rest of the package reads, so check they
// actually came out of the registry rather than being left empty by an init
// ordering mistake.
func TestDerivedTablesMatchTheRegistry(t *testing.T) {
	if len(typeTable) != len(frameSpecs) {
		t.Errorf("typeTable has %d entries, frameSpecs has %d", len(typeTable), len(frameSpecs))
	}

	if len(catchUpOrder) != len(syncableSpecs) {
		t.Errorf("catchUpOrder has %d entries, %d types are syncable", len(catchUpOrder), len(syncableSpecs))
	}

	if len(allowedCatchUpFrames) != len(syncableSpecs) {
		t.Errorf("allowedCatchUpFrames has %d entries, %d types are syncable",
			len(allowedCatchUpFrames), len(syncableSpecs))
	}

	if len(syncableTypes) != len(syncableSpecs) {
		t.Errorf("syncableTypes has %d entries, syncableSpecs has %d",
			len(syncableTypes), len(syncableSpecs))
	}

	// catchUpOrder ranks must be a dense 0..n-1, since Less compares them
	// directly and a gap would mean a type was skipped while building them.
	ranks := map[int]bool{}
	for typ, rank := range catchUpOrder {
		if rank < 0 || rank >= len(syncableSpecs) {
			t.Errorf("frame type %d has catch up rank %d, outside 0..%d",
				typ, rank, len(syncableSpecs)-1)
		}
		if ranks[rank] {
			t.Errorf("catch up rank %d is used by more than one frame type", rank)
		}
		ranks[rank] = true
	}

	for _, spec := range syncableSpecs {
		if typeTable[spec.typ] != spec.table {
			t.Errorf("typeTable[%d] is %q, registry says %q",
				spec.typ, typeTable[spec.typ], spec.table)
		}
		if !allowedCatchUpFrames[spec.typ] {
			t.Errorf("syncable frame type %d is missing from allowedCatchUpFrames", spec.typ)
		}
	}
}

// The order of syncableSpecs is the replay order of a catch up, and some of it
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
