package main

import "math"

// Percentiles are estimated with a DDSketch-style log-bucketed histogram
// (Masson, Rim & Lee, "DDSketch", VLDB 2019). Exact percentiles need every
// value kept and sorted, which breaks constant memory; the sketch keeps only
// a count per bucket instead.
//
// Bucket i holds the positive values in (gamma^(i-1), gamma^i]. Because the
// buckets grow by a fixed ratio, reporting a bucket by one representative
// value is wrong by at most relativeAccuracy of the true value, whatever the
// scale: 0.5 ms and 4 GB get the same ±1% guarantee.

// relativeAccuracy is the guaranteed bound on |estimate - true| / |true|.
const relativeAccuracy = 0.01

// minIndexable is the smallest magnitude given its own bucket. Smaller
// values count as zero; without a floor, values like 1e-300 would need tens
// of thousands of buckets between them and 1.
const minIndexable = 1e-9

var (
	gamma    = (1 + relativeAccuracy) / (1 - relativeAccuracy)
	logGamma = math.Log(gamma)
)

// bucketIndex returns the bucket for a magnitude x >= minIndexable.
func bucketIndex(x float64) int {
	return int(math.Ceil(math.Log(x) / logGamma))
}

// bucketValue is the value reported for bucket i. Of all the values in
// (gamma^(i-1), gamma^i], it's the one with the smallest worst-case
// relative error.
func bucketValue(i int) float64 {
	return 2 * math.Pow(gamma, float64(i)) / (gamma + 1)
}

// bucketStore counts values per bucket index in a dense slice covering
// indexes offset .. offset+len(counts)-1. It only grows to cover the indexes
// actually seen, so memory depends on the data's range, not its size.
type bucketStore struct {
	offset int
	counts []int
}

func (s *bucketStore) add(index int) {
	switch {
	case len(s.counts) == 0:
		s.offset = index
		s.counts = []int{0}
	case index < s.offset:
		// Grow at the front: allocate a bigger slice and copy the old
		// counts to the end. copy (not append) because append only grows
		// at the back.
		grown := make([]int, len(s.counts)+s.offset-index)
		copy(grown[s.offset-index:], s.counts)
		s.counts = grown
		s.offset = index
	case index >= s.offset+len(s.counts):
		// Grow at the back; append may reuse spare capacity.
		s.counts = append(s.counts, make([]int, index-s.offset-len(s.counts)+1)...)
	}
	s.counts[index-s.offset]++
}

// sketch estimates percentiles of a stream of values. The zero value is an
// empty sketch, ready to use.
type sketch struct {
	pos   bucketStore // values >= minIndexable
	neg   bucketStore // values <= -minIndexable, by magnitude
	zeros int         // values with |v| < minIndexable
	count int
}

func (s *sketch) add(v float64) {
	switch {
	case v >= minIndexable:
		s.pos.add(bucketIndex(v))
	case v <= -minIndexable:
		s.neg.add(bucketIndex(-v))
	default:
		s.zeros++
	}
	s.count++
}

// nearestRank returns the 0-based sorted position of the q-quantile of n
// values, using the nearest-rank definition: the smallest value that at
// least q of all values are <= to. For p99 of 5 values that is the largest
// one; rounding down instead would understate the tail.
func nearestRank(q float64, n int) int {
	// The 1e-9 absorbs float error such as 0.95*20 = 19.000000000000004.
	r := int(math.Ceil(q*float64(n)-1e-9)) - 1
	return min(max(r, 0), n-1)
}

// quantile returns an estimate of the q-quantile (0 <= q <= 1), within
// relativeAccuracy of the exact nearest-rank value. It returns NaN for an
// empty sketch.
func (s *sketch) quantile(q float64) float64 {
	if s.count == 0 {
		return math.NaN()
	}
	rank := nearestRank(q, s.count)

	// Walk the buckets in ascending value order and stop at the first one
	// that holds the value at position rank: most negative first (largest
	// magnitude), then zero, then positives.
	seen := 0
	for i := len(s.neg.counts) - 1; i >= 0; i-- {
		seen += s.neg.counts[i]
		if seen > rank {
			return -bucketValue(i + s.neg.offset)
		}
	}
	seen += s.zeros
	if seen > rank {
		return 0
	}
	for i, c := range s.pos.counts {
		seen += c
		if seen > rank {
			return bucketValue(i + s.pos.offset)
		}
	}
	// Unreachable: rank <= count-1, and seen reaches count.
	return math.NaN()
}
