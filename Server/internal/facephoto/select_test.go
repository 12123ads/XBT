package facephoto

import (
	"errors"
	"testing"
)

type fixedRand struct {
	intResult int
	float     float64
}

func (r fixedRand) Float64() float64 { return r.float }
func (r fixedRand) IntN(n int) int {
	if r.intResult >= n {
		return n - 1
	}
	return r.intResult
}

func TestSelectEmptyPool(t *testing.T) {
	if _, err := Select(nil, false, fixedRand{}); !errors.Is(err, ErrNoPhotos) {
		t.Fatalf("err = %v, want ErrNoPhotos", err)
	}
}

func TestSelectPrefersUnfailedUsedAfterFailure(t *testing.T) {
	photos := []Candidate{
		{ID: 1, UseCount: 4, IsFailureBefore: true},
		{ID: 2, UseCount: 2},
		{ID: 3, UseCount: 0},
	}
	got, err := Select(photos, true, fixedRand{intResult: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 2 {
		t.Fatalf("selected photo = %d, want unfailed used photo 2", got.ID)
	}
}

func TestSelectFallsBackToNeverFailedWhenNoUsedCandidate(t *testing.T) {
	photos := []Candidate{
		{ID: 1, UseCount: 3, IsFailureBefore: true},
		{ID: 2, UseCount: 0},
		{ID: 3, UseCount: 0},
	}
	got, err := Select(photos, true, fixedRand{intResult: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 3 {
		t.Fatalf("selected photo = %d, want never-failed photo 3", got.ID)
	}
}

func TestSelectFallsBackToWholePoolWhenEveryPhotoFailed(t *testing.T) {
	photos := []Candidate{
		{ID: 1, UseCount: 1, IsFailureBefore: true},
		{ID: 2, UseCount: 5, IsFailureBefore: true},
	}
	got, err := Select(photos, true, fixedRand{intResult: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 2 {
		t.Fatalf("selected photo = %d, want fallback photo 2", got.ID)
	}
}

func TestSelectWithoutFailureHistoryUsesWholePool(t *testing.T) {
	photos := []Candidate{{ID: 1}, {ID: 2, UseCount: 9}}
	got, err := Select(photos, false, fixedRand{intResult: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 2 {
		t.Fatalf("selected photo = %d, want pool member 2", got.ID)
	}
}
