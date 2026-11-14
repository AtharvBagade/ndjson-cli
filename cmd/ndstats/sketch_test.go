package main

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// exactQuantile is the reference the sketch is checked against: the exact
// nearest-rank value. It sorts a copy (slices.Clone): sorting the caller's
// slice directly would reorder it, because a slice shares its backing array.
func exactQuantile(values []float64, q float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[nearestRank(q, len(sorted))]
}

func TestNearestRank(t *testing.T) {
	tests := []struct {
		q    float64
		n    int
		want int
	}{
		{q: 0, n: 5, want: 0},
		{q: 0.5, n: 5, want: 2},  // 3rd of 5
		{q: 0.5, n: 4, want: 1},  // 2nd of 4: at least half are <= it
		{q: 0.99, n: 5, want: 4}, // the largest: the tail is not understated
		{q: 1, n: 5, want: 4},
		{q: 0.95, n: 20, want: 18}, // 0.95*20 is 19.000000000000004 in float64
		{q: 0.5, n: 1, want: 0},
	}
	for _, tc := range tests {
		if got := nearestRank(tc.q, tc.n); got != tc.want {
			t.Errorf("nearestRank(%v, %d) = %d; want %d", tc.q, tc.n, got, tc.want)
		}
	}
}

func TestSketchAccuracy(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	gen := func(n int, f func() float64) []float64 {
		vs := make([]float64, n)
		for i := range vs {
			vs[i] = f()
		}
		return vs
	}

	tests := []struct {
		name   string
		values []float64
	}{
		{name: "log-normal latencies", values: gen(50_000, func() float64 { return math.Exp(3 + 0.9*rng.NormFloat64()) })},
		{name: "uniform 0 to 1000", values: gen(50_000, func() float64 { return rng.Float64() * 1000 })},
		{name: "byte sizes with many zeros", values: gen(50_000, func() float64 {
			if rng.IntN(4) == 0 {
				return 0
			}
			return float64(rng.IntN(64 << 20))
		})},
		{name: "negative and positive", values: gen(50_000, func() float64 { return rng.NormFloat64() * 100 })},
		{name: "all negative", values: gen(10_000, func() float64 { return -1 - rng.Float64()*500 })},
		{name: "small integers", values: gen(10_000, func() float64 { return float64(1 + rng.IntN(10)) })},
		{name: "huge range", values: gen(10_000, func() float64 { return math.Pow(10, rng.Float64()*18-6) })},
		{name: "single value", values: []float64{42}},
		{name: "two values", values: []float64{1, 100}},
		{name: "five values (p99 is the max)", values: []float64{3.2, 9.7, 12.4, 250, 980}},
		{name: "all equal", values: gen(1000, func() float64 { return 7.5 })},
	}
	qs := []float64{0, 0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.95, 0.99, 0.999, 1}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s sketch
			for _, v := range tc.values {
				s.add(v)
			}
			for _, q := range qs {
				got, want := s.quantile(q), exactQuantile(tc.values, q)
				if !withinRelative(got, want, relativeAccuracy) {
					t.Errorf("quantile(%v) = %v; exact %v (relative error %.4f > %v)",
						q, got, want, math.Abs(got-want)/math.Abs(want), relativeAccuracy)
				}
			}
		})
	}
}

// withinRelative reports whether got is within tol of want, relative to
// want. Zero must be reported as exactly zero.
func withinRelative(got, want, tol float64) bool {
	if want == 0 {
		return got == 0
	}
	// A tiny allowance for float rounding in Log/Pow at bucket edges.
	return math.Abs(got-want) <= math.Abs(want)*tol*(1+1e-9)
}

func TestSketchEmpty(t *testing.T) {
	var s sketch
	if got := s.quantile(0.5); !math.IsNaN(got) {
		t.Errorf("quantile of empty sketch = %v; want NaN", got)
	}
}

func TestSketchTinyValuesCountAsZero(t *testing.T) {
	var s sketch
	s.add(1e-12)
	s.add(-1e-12)
	s.add(0)
	if s.zeros != 3 || len(s.pos.counts) != 0 || len(s.neg.counts) != 0 {
		t.Errorf("zeros = %d, pos buckets = %d, neg buckets = %d; want 3, 0, 0", s.zeros, len(s.pos.counts), len(s.neg.counts))
	}
}

func TestBucketStoreGrowsBothWays(t *testing.T) {
	var b bucketStore
	b.add(10)
	b.add(12) // grows at the back
	b.add(7)  // grows at the front: existing counts must move, not be lost
	b.add(10)

	if b.offset != 7 || len(b.counts) != 6 {
		t.Fatalf("offset, len = %d, %d; want 7, 6", b.offset, len(b.counts))
	}
	want := []int{1, 0, 0, 2, 0, 1} // indexes 7..12
	if !slices.Equal(b.counts, want) {
		t.Errorf("counts = %v; want %v", b.counts, want)
	}
}

func TestSketchMemoryIsBounded(t *testing.T) {
	// A million values over 0.5 ms .. 10 s need only a few hundred buckets:
	// memory depends on the range of the data, not how much of it there is.
	var s sketch
	rng := rand.New(rand.NewPCG(1, 1))
	for range 1_000_000 {
		s.add(0.5 + rng.Float64()*10_000)
	}
	if n := len(s.pos.counts); n > 600 {
		t.Errorf("buckets = %d for range 0.5..10000; want a few hundred", n)
	}
}
