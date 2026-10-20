package facephoto

import (
	"errors"
	mathrand "math/rand/v2"
)

// MaxPhotos is the per-account pool limit (matches CSF MAX_FACE_IMAGES).
const MaxPhotos = 5

// ErrNoPhotos reports that an account has no managed face photo.
var ErrNoPhotos = errors.New("face photo pool is empty")

// Candidate is the subset of a stored photo needed to choose the next one.
type Candidate struct {
	ID              uint
	UseCount        int
	IsFailureBefore bool
}

// Select picks one photo uniformly. When the account has a failure history,
// photos that never failed and were already used are preferred; if none exist
// the choice falls back to never-failed photos, then to the whole pool. The
// random source is injectable so tests can assert the exact choice.
func Select(photos []Candidate, preferUnfailedUsed bool, rnd RandSource) (Candidate, error) {
	if len(photos) == 0 {
		return Candidate{}, ErrNoPhotos
	}
	if rnd == nil {
		seed := mathrand.Uint64()
		rnd = mathrand.New(mathrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	}
	pool := photos
	if preferUnfailedUsed {
		if preferred := filterCandidates(photos, func(p Candidate) bool {
			return !p.IsFailureBefore && p.UseCount > 0
		}); len(preferred) > 0 {
			pool = preferred
		} else if neverFailed := filterCandidates(photos, func(p Candidate) bool {
			return !p.IsFailureBefore
		}); len(neverFailed) > 0 {
			pool = neverFailed
		}
	}
	return pool[rnd.IntN(len(pool))], nil
}

func filterCandidates(photos []Candidate, keep func(Candidate) bool) []Candidate {
	filtered := make([]Candidate, 0, len(photos))
	for _, photo := range photos {
		if keep(photo) {
			filtered = append(filtered, photo)
		}
	}
	return filtered
}
