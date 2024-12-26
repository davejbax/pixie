package align

func Address[N uint32 | uint64](addr N, alignment N) N {
	if alignment == 0 {
		return addr
	}

	return ((addr + alignment - 1) / alignment) * alignment
}
