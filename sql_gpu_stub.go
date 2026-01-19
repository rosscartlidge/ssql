//go:build !gpu

package ssql

// GPUMinGroupSize is the minimum group size for GPU acceleration.
// In the stub implementation, this is unused but provided for API compatibility.
const GPUMinGroupSize = 10000

// SumGPU returns a sum aggregation function.
// Without GPU support, this is identical to Sum().
func SumGPU(field string) AggregateFunc {
	return Sum(field)
}

// AvgGPU returns an average aggregation function.
// Without GPU support, this is identical to Avg().
func AvgGPU(field string) AggregateFunc {
	return Avg(field)
}

// GPUAvailable returns false when GPU support is not compiled in.
func GPUAvailable() bool {
	return false
}
