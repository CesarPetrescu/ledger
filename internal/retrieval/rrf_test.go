package retrieval

import "testing"

func TestRRFKnownRanksAndStableTies(t *testing.T) {
	got := RRF([]Ranked{{Ref: "a", Ord: 0}, {Ref: "b", Ord: 0}}, []Ranked{{Ref: "b", Ord: 0}, {Ref: "a", Ord: 0}, {Ref: "c", Ord: 0}}, 60)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %d results", len(got))
	}
	for i := range want {
		if got[i].Ref != want[i] {
			t.Fatalf("rank %d = %s, want %s", i, got[i].Ref, want[i])
		}
	}
}

func TestRRFIgnoresSourceScoreScales(t *testing.T) {
	got := RRF([]Ranked{{Ref: "first", Score: -1000}, {Ref: "second", Score: 1000}}, nil, 60)
	if len(got) != 2 || got[0].Ref != "first" || got[1].Ref != "second" {
		t.Fatalf("RRF retained source scores: %#v", got)
	}
}
