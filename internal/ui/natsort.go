package ui

// NaturalLess compares strings so embedded numbers order numerically:
// "key:2" < "key:10" < "key:100". Digit runs are compared by value
// (ignoring leading zeros), everything else byte-wise.
func NaturalLess(a, b string) bool {
	for len(a) > 0 && len(b) > 0 {
		if isDigit(a[0]) && isDigit(b[0]) {
			aNum, aRest := takeDigits(a)
			bNum, bRest := takeDigits(b)
			at, bt := trimZeros(aNum), trimZeros(bNum)
			if len(at) != len(bt) {
				return len(at) < len(bt)
			}
			if at != bt {
				return at < bt
			}
			a, b = aRest, bRest
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func takeDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

func trimZeros(s string) string {
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}
