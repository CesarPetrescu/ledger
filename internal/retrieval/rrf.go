package retrieval

import "sort"

type Ranked struct {
	Ref         string  `json:"ref"`
	Ord         int     `json:"-"`
	Kind        string  `json:"kind"`
	Snippet     string  `json:"snippet"`
	ProjectSlug string  `json:"project_slug"`
	Score       float64 `json:"score"`
}

func RRF(first, second []Ranked, k int) []Ranked {
	scores := map[string]Ranked{}
	for _, list := range [][]Ranked{first, second} {
		for rank, item := range list {
			key := item.Ref + "\x00" + string(rune(item.Ord))
			current, exists := scores[key]
			if !exists {
				current = item
				current.Score = 0
			}
			current.Score += 1 / float64(k+rank+1)
			scores[key] = current
		}
	}
	out := make([]Ranked, 0, len(scores))
	for _, item := range scores {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Ref != out[j].Ref {
			return out[i].Ref < out[j].Ref
		}
		return out[i].Ord < out[j].Ord
	})
	return out
}
