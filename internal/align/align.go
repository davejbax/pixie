// Package align contains utilities for aligning virtual/physical addresses
package align

// Address aligns the given address to a multiple of alignment
func Address[N uint32 | uint64 | int](addr N, alignment N) N {
	if alignment == 0 {
		return addr
	}

	return ((addr + alignment - 1) / alignment) * alignment
}
