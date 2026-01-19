//go:build gpu

package ssql

import (
	"github.com/rosscartlidge/ssql/v4/gpu"
)

// GPUMinGroupSize is the minimum group size for GPU acceleration.
// Groups smaller than this will use CPU (GPU transfer overhead dominates).
const GPUMinGroupSize = 10000

// SumGPU returns a GPU-accelerated sum aggregation function.
// Falls back to CPU for small groups or when GPU is unavailable.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "total": ssql.SumGPU("amount"),
//	}
func SumGPU(field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		// Fall back to CPU for small groups
		if len(records) < GPUMinGroupSize || !gpu.Available() {
			return Sum(field)(records)
		}

		// Extract float64 values from records
		data := make([]float64, 0, len(records))
		for _, record := range records {
			if value, ok := Get[float64](record, field); ok {
				data = append(data, value)
			}
		}

		if len(data) == 0 {
			return AggResult[float64]{val: 0.0}
		}

		// Use GPU for sum
		result, err := gpu.SumFloat64(data)
		if err != nil {
			// Fall back to CPU on error
			return Sum(field)(records)
		}

		return AggResult[float64]{val: result}
	}
}

// AvgGPU returns a GPU-accelerated average aggregation function.
// Falls back to CPU for small groups or when GPU is unavailable.
//
// Example:
//
//	aggregations := map[string]ssql.AggregateFunc{
//	    "avg_salary": ssql.AvgGPU("salary"),
//	}
func AvgGPU(field string) AggregateFunc {
	return func(records []Record) AggregateResult {
		// Fall back to CPU for small groups
		if len(records) < GPUMinGroupSize || !gpu.Available() {
			return Avg(field)(records)
		}

		// Extract float64 values from records
		data := make([]float64, 0, len(records))
		for _, record := range records {
			if value, ok := Get[float64](record, field); ok {
				data = append(data, value)
			}
		}

		if len(data) == 0 {
			return AggResult[float64]{val: 0.0}
		}

		// Use GPU for sum, then divide by count
		sum, err := gpu.SumFloat64(data)
		if err != nil {
			// Fall back to CPU on error
			return Avg(field)(records)
		}

		return AggResult[float64]{val: sum / float64(len(data))}
	}
}

// GPUAvailable returns true if GPU acceleration is available.
func GPUAvailable() bool {
	return gpu.Available()
}
