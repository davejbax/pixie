package math

func GreatestCommonDivisor(a int, b int) int {
	for b != 0 {
		b, a = a%b, b
	}

	return a
}

func LowestCommonMultiple(a int, b int) int {
	return (a * b) / GreatestCommonDivisor(a, b)
}
