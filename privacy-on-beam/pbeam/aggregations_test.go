//
// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package pbeam

import (
	"reflect"
	"testing"

	"github.com/google/differential-privacy/go/noise"
	"github.com/google/differential-privacy/privacy-on-beam/pbeam/testutils"
	"github.com/apache/beam/sdks/go/pkg/beam"
	"github.com/apache/beam/sdks/go/pkg/beam/testing/ptest"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestNewBoundedSumFn(t *testing.T) {
	opts := []cmp.Option{
		cmpopts.EquateApprox(0, 1e-10),
		cmpopts.IgnoreUnexported(boundedSumFloat64Fn{}, boundedSumInt64Fn{}),
	}
	for _, tc := range []struct {
		desc      string
		noiseKind noise.Kind
		vKind     reflect.Kind
		want      interface{}
	}{
		{"Laplace Float64", noise.LaplaceNoise, reflect.Float64,
			&boundedSumFloat64Fn{
				NoiseEpsilon:              0.5,
				PartitionSelectionEpsilon: 0.5,
				NoiseDelta:                0,
				PartitionSelectionDelta:   1e-5,
				MaxPartitionsContributed:  17,
				Lower:                     0,
				Upper:                     10,
				NoiseKind:                 noise.LaplaceNoise,
				PublicPartitions:          false,
			}},
		{"Gaussian Float64", noise.GaussianNoise, reflect.Float64,
			&boundedSumFloat64Fn{
				NoiseEpsilon:              0.5,
				PartitionSelectionEpsilon: 0.5,
				NoiseDelta:                5e-6,
				PartitionSelectionDelta:   5e-6,
				MaxPartitionsContributed:  17,
				Lower:                     0,
				Upper:                     10,
				NoiseKind:                 noise.GaussianNoise,
				PublicPartitions:          false,
			}},
		{"Laplace Int64", noise.LaplaceNoise, reflect.Int64,
			&boundedSumInt64Fn{
				NoiseEpsilon:              0.5,
				PartitionSelectionEpsilon: 0.5,
				NoiseDelta:                0,
				PartitionSelectionDelta:   1e-5,
				MaxPartitionsContributed:  17,
				Lower:                     0,
				Upper:                     10,
				NoiseKind:                 noise.LaplaceNoise,
				PublicPartitions:          false,
			}},
		{"Gaussian Int64", noise.GaussianNoise, reflect.Int64,
			&boundedSumInt64Fn{
				NoiseEpsilon:              0.5,
				PartitionSelectionEpsilon: 0.5,
				NoiseDelta:                5e-6,
				PartitionSelectionDelta:   5e-6,
				MaxPartitionsContributed:  17,
				Lower:                     0,
				Upper:                     10,
				NoiseKind:                 noise.GaussianNoise,
				PublicPartitions:          false,
			}},
	} {
		got := newBoundedSumFn(1, 1e-5, 17, 0, 10, tc.noiseKind, tc.vKind, false, disabled)
		if diff := cmp.Diff(tc.want, got, opts...); diff != "" {
			t.Errorf("newBoundedSumFn mismatch for '%s' (-want +got):\n%s", tc.desc, diff)
		}
	}
}

func TestBoundedSumFloat64FnSetup(t *testing.T) {
	for _, tc := range []struct {
		desc      string
		noiseKind noise.Kind
		wantNoise interface{}
	}{
		{"Laplace noise kind", noise.LaplaceNoise, noise.Laplace()},
		{"Gaussian noise kind", noise.GaussianNoise, noise.Gaussian()}} {
		got := newBoundedSumFloat64Fn(1, 1e-5, 17, 0, 10, tc.noiseKind, false, disabled)
		got.Setup()
		if !cmp.Equal(tc.wantNoise, got.noise) {
			t.Errorf("Setup: for %s got %v, want %v", tc.desc, got.noise, tc.wantNoise)
		}
	}
}

func TestBoundedSumInt64FnSetup(t *testing.T) {
	for _, tc := range []struct {
		desc      string
		noiseKind noise.Kind
		wantNoise interface{}
	}{
		{"Laplace noise kind", noise.LaplaceNoise, noise.Laplace()},
		{"Gaussian noise kind", noise.GaussianNoise, noise.Gaussian()}} {
		got := newBoundedSumInt64Fn(1, 1e-5, 17, 0, 10, tc.noiseKind, false, disabled)
		got.Setup()
		if !cmp.Equal(tc.wantNoise, got.noise) {
			t.Errorf("Setup: for %s got %v, want %v", tc.desc, got.noise, tc.wantNoise)
		}
	}
}

func TestBoundedSumInt64FnAddInput(t *testing.T) {
	// Since δ=0.5 and 2 entries are added, PreAggPartitionSelection always emits.
	// Since ε=1e100, the noise is added with probability in the order of exp(-1e100),
	// which means we don't have to worry about tolerance/flakiness calculations.
	fn := newBoundedSumInt64Fn(1e100, 0.5, 1, 0, 2, noise.LaplaceNoise, false, disabled)
	fn.Setup()

	accum := fn.CreateAccumulator()
	fn.AddInput(accum, 2)
	fn.AddInput(accum, 2)

	got := fn.ExtractOutput(accum)
	want := testutils.Int64Ptr(4)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unexpected output (-want +got):\n%s", diff)
	}
}

func TestBoundedSumInt64FnMergeAccumulators(t *testing.T) {
	// We use δ=0.5 so that partition selection is non-deterministic with 1 input
	// and deterministic with 2 inputs. This is used to verify that merging
	// accumulators is also affecting our partition selection outcome.
	//
	// Since ε=1e100, the noise is added with probability in the order of exp(-1e100),
	// which means we don't have to worry about tolerance/flakiness calculations.
	fn := newBoundedSumInt64Fn(1e100, 0.5, 1, 0, 2, noise.LaplaceNoise, false, disabled)
	fn.Setup()

	accum1 := fn.CreateAccumulator()
	fn.AddInput(accum1, 2)
	accum2 := fn.CreateAccumulator()
	fn.AddInput(accum2, 1)
	fn.MergeAccumulators(accum1, accum2)

	got := fn.ExtractOutput(accum1)
	want := testutils.Int64Ptr(3)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("unexpected output (-want +got):\n%s", diff)
	}
}

func TestBoundedSumInt64FnExtractOutputReturnsNilForSmallPartitions(t *testing.T) {
	for _, tc := range []struct {
		desc      string
		inputSize int
	}{
		// It's a special case for partition selection in which the algorithm should always eliminate the partition.
		{"Empty input", 0},
		// The probability of keeping a partition with 1 privacy unit is equal to δ=1e-23 which results in a flakiness of 10⁻²³.
		{"Input with 1 privacy unit", 1}} {

		fn := newBoundedSumInt64Fn(1, 1e-23, 1, 0, 2, noise.LaplaceNoise, false, disabled)
		fn.Setup()
		accum := fn.CreateAccumulator()
		for i := 0; i < tc.inputSize; i++ {
			fn.AddInput(accum, 1)
		}

		got := fn.ExtractOutput(accum)

		// Should return nil output for small partitions.
		if got != nil {
			t.Errorf("ExtractOutput: for %s got: %d, want nil", tc.desc, *got)
		}
	}
}

func TestBoundedSumInt64FnExtractOutputWithPublicPartitionsDoesNotThreshold(t *testing.T) {
	for _, tc := range []struct {
		desc      string
		inputSize int
	}{
		{"Empty input", 0},
		{"Input with 1 user", 1},
		{"Input with 10 users", 10},
		{"Input with 100 users", 100}} {

		fn := newBoundedSumInt64Fn(1, 0, 1, 0, 2, noise.LaplaceNoise, true, disabled)
		fn.Setup()
		accum := fn.CreateAccumulator()
		for i := 0; i < tc.inputSize; i++ {
			fn.AddInput(accum, 1)
		}

		got := fn.ExtractOutput(accum)

		if got == nil {
			t.Errorf("ExtractOutput for %s thresholded with public partitions when it shouldn't", tc.desc)
		}
	}
}

func TestBoundedSumFloat64FnAddInput(t *testing.T) {
	// Since δ=0.5 and 2 entries are added, PreAggPartitionSelection always emits.
	// Since ε=1e100, added noise is negligible.
	fn := newBoundedSumFloat64Fn(1e100, 0.5, 1, 0, 2, noise.LaplaceNoise, false, disabled)
	fn.Setup()

	accum := fn.CreateAccumulator()
	fn.AddInput(accum, 2)
	fn.AddInput(accum, 2)

	got := fn.ExtractOutput(accum)
	want := testutils.Float64Ptr(4)
	if diff := cmp.Diff(want, got, cmpopts.EquateApprox(0, testutils.LaplaceTolerance(23, 2, 1e100))); diff != "" {
		t.Errorf("unexpected output (-want +got):\n%s", diff)
	}
}

func TestBoundedSumFloat64FnMergeAccumulators(t *testing.T) {
	// We use δ=0.5 so that partition selection is non-deterministic with 1 input
	// and deterministic with 2 inputs. This is used to verify that merging
	// accumulators is also effecting our partition selection outcome.
	//
	// Since ε=1e100, added noise is negligible.
	fn := newBoundedSumFloat64Fn(1e100, 0.5, 1, 0, 2, noise.LaplaceNoise, false, disabled)
	fn.Setup()

	accum1 := fn.CreateAccumulator()
	fn.AddInput(accum1, 2)
	accum2 := fn.CreateAccumulator()
	fn.AddInput(accum2, 1)
	fn.MergeAccumulators(accum1, accum2)

	got := fn.ExtractOutput(accum1)
	want := testutils.Float64Ptr(3)
	if diff := cmp.Diff(want, got, cmpopts.EquateApprox(0, testutils.LaplaceTolerance(23, 2, 1e100))); diff != "" {
		t.Errorf("unexpected output (-want +got):\n%s", diff)
	}
}

func TestBoundedSumFloat64FnExtractOutputReturnsNilForSmallPartitions(t *testing.T) {
	for _, tc := range []struct {
		desc      string
		inputSize int
	}{
		// It's a special case for partition selection in which the algorithm should always eliminate the partition.
		{"Empty input", 0},
		// The probability of keeping a partition with 1 privacy unit is equal to δ=1e-23 which results in a flakiness of 10⁻²³.
		{"Input with 1 privacy unit", 1}} {

		fn := newBoundedSumFloat64Fn(1, 1e-23, 1, 0, 2, noise.LaplaceNoise, false, disabled)
		fn.Setup()
		accum := fn.CreateAccumulator()
		for i := 0; i < tc.inputSize; i++ {
			fn.AddInput(accum, 1)
		}

		got := fn.ExtractOutput(accum)

		// Should return nil output for small partitions.
		if got != nil {
			t.Errorf("ExtractOutput: for %s got: %f, want nil", tc.desc, *got)
		}
	}
}

func TestBoundedSumFloat64FnExtractOutputWithPublicPartitionsDoesNotThreshold(t *testing.T) {
	for _, tc := range []struct {
		desc      string
		inputSize int
	}{
		{"Empty input", 0},
		{"Input with 1 user", 1},
		{"Input with 10 users", 10},
		{"Input with 100 users", 100}} {
		publicPartitions := true
		fn := newBoundedSumFloat64Fn(1, 0, 1, 0, 2, noise.LaplaceNoise, publicPartitions, disabled)
		fn.Setup()
		accum := fn.CreateAccumulator()
		for i := 0; i < tc.inputSize; i++ {
			fn.AddInput(accum, 1)
		}

		got := fn.ExtractOutput(accum)
		if got == nil {
			t.Errorf("ExtractOutput for %s thresholded with public partitions when it shouldn't", tc.desc)
		}
	}
}

// Checks that elements with non-public partitions are dropped.
// This function is used for count and distinct_id.
func TestDropNonPublicPartitionsVFn(t *testing.T) {
	pairs := testutils.ConcatenatePairs(
		testutils.MakePairsWithFixedV(7, 0),
		testutils.MakePairsWithFixedV(52, 1),
		testutils.MakePairsWithFixedV(99, 2),
		testutils.MakePairsWithFixedV(10, 3),
	)

	// Keep partitions 0, 2;
	// drop partitions 1, 3.
	result := testutils.ConcatenatePairs(
		testutils.MakePairsWithFixedV(7, 0),
		testutils.MakePairsWithFixedV(99, 2),
		testutils.MakePairsWithFixedV(99, 2),
	)

	_, s, col, want := ptest.CreateList2(pairs, result)
	want = beam.ParDo(s, testutils.PairToKV, want)
	col = beam.ParDo(s, testutils.PairToKV, col)
	partitions := []int{0, 2}

	partitionsCol := beam.CreateList(s, partitions)
	epsilon, delta := 50.0, 1e-200
	pcol := MakePrivate(s, col, NewPrivacySpec(epsilon, delta))
	_, partitionT := beam.ValidateKVType(pcol.col)
	partitionEncodedType := beam.EncodedType{partitionT.Type()}
	got := dropNonPublicPartitionsVFn(s, partitionsCol, pcol, partitionEncodedType)
	if err := testutils.EqualsKVInt(s, got, want); err != nil {
		t.Fatalf("dropNonPublicPartitionsVFn: for %v got: %v, want %v", col, got, want)
	}
}

// TestDropNonPublicPartitionsKVFn checks that int elements with non-public partitions
// are dropped (tests function used for sum and mean).
func TestDropNonPublicPartitionsKVFn(t *testing.T) {
	triples := testutils.ConcatenateTriplesWithIntValue(
		testutils.MakeDummyTripleWithIntValue(7, 0),
		testutils.MakeDummyTripleWithIntValue(58, 1),
		testutils.MakeDummyTripleWithIntValue(99, 2),
		testutils.MakeDummyTripleWithIntValue(45, 100),
		testutils.MakeDummyTripleWithIntValue(20, 33))
	// Keep partitions 0, 2.
	// Drop partitions 1, 33, 100.
	result := testutils.ConcatenateTriplesWithIntValue(
		testutils.MakeDummyTripleWithIntValue(7, 0),
		testutils.MakeDummyTripleWithIntValue(99, 2))

	_, s, col, col2 := ptest.CreateList2(triples, result)
	// Doesn't matter that the values 3, 4, 5, 6, 9, 10
	// are in the partitions PCollection because we are
	// just dropping the values that are in our original PCollection
	// that are not in public partitions.
	partitionsCol := beam.CreateList(s, []int{0, 2, 3, 4, 5, 6, 9, 10})
	col = beam.ParDo(s, testutils.ExtractIDFromTripleWithIntValue, col)
	col2 = beam.ParDo(s, testutils.ExtractIDFromTripleWithIntValue, col2)
	epsilon, delta := 50.0, 1e-200

	pcol := MakePrivate(s, col, NewPrivacySpec(epsilon, delta))
	pcol = ParDo(s, testutils.TripleWithIntValueToKV, pcol)
	got := dropNonPublicPartitionsKVFn(s, partitionsCol, pcol, pcol.codec.KType)
	got = beam.SwapKV(s, got)

	pcol2 := MakePrivate(s, col2, NewPrivacySpec(epsilon, delta))
	pcol2 = ParDo(s, testutils.TripleWithIntValueToKV, pcol2)
	want := pcol2.col
	want = beam.SwapKV(s, want)

	if err := testutils.EqualsKVInt(s, got, want); err != nil {
		t.Fatalf("dropPublicPartitionsKVFn: for %v got: %v, want %v", col, got, want)
	}
}

// Check that float elements with non-public partitions
// are dropped (tests function used for sum and mean).
func TestDropNonPublicPartitionsFloat(t *testing.T) {
	// In this test, we check  that non-public partitions
	// are dropped. This function is used for sum and mean.
	// Used example values from the mean test.
	triples := testutils.ConcatenateTriplesWithFloatValue(
		testutils.MakeTripleWithFloatValue(7, 0, 2.0),
		testutils.MakeTripleWithFloatValueStartingFromKey(7, 100, 1, 1.3),
		testutils.MakeTripleWithFloatValueStartingFromKey(107, 150, 1, 2.5),
	)
	// Keep partition 0.
	// drop partition 1.
	result := testutils.ConcatenateTriplesWithFloatValue(
		testutils.MakeTripleWithFloatValue(7, 0, 2.0))

	_, s, col, col2 := ptest.CreateList2(triples, result)

	// Doesn't matter that the values 2, 3, 4, 5, 6, 7 are in the partitions PCollection.
	// We are just dropping the values that are in our original PCollection that are not in
	// public partitions.
	partitionsCol := beam.CreateList(s, []int{0, 2, 3, 4, 5, 6, 7})
	col = beam.ParDo(s, testutils.ExtractIDFromTripleWithFloatValue, col)
	col2 = beam.ParDo(s, testutils.ExtractIDFromTripleWithFloatValue, col2)
	epsilon, delta := 50.0, 1e-200

	pcol := MakePrivate(s, col, NewPrivacySpec(epsilon, delta))
	pcol = ParDo(s, testutils.TripleWithFloatValueToKV, pcol)
	got := dropNonPublicPartitionsKVFn(s, partitionsCol, pcol, pcol.codec.KType)
	got = beam.SwapKV(s, got)

	pcol2 := MakePrivate(s, col2, NewPrivacySpec(epsilon, delta))
	pcol2 = ParDo(s, testutils.TripleWithFloatValueToKV, pcol2)
	want := pcol2.col
	want = beam.SwapKV(s, want)

	if err := testutils.EqualsKVInt(s, got, want); err != nil {
		t.Fatalf("DropNonPublicPartitionsFloat: for %v got: %v, want %v", col, got, want)
	}
}
