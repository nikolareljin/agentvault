package textutil

// TruncateRunesWithEllipsis truncates s to maxRunes runes and appends "..."
// when truncation is required and maxRunes allows it.
func TruncateRunesWithEllipsis(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) == 0 {
		return ""
	}
	if maxRunes <= 3 {
		return TruncateRunes(s, maxRunes)
	}

	keepRunes := maxRunes - 3
	cutoff := len(s)
	runeCount := 0
	for idx := range s {
		if runeCount == keepRunes {
			cutoff = idx
		}
		runeCount++
		if runeCount > maxRunes {
			return s[:cutoff] + "..."
		}
	}

	return s
}

// TruncateRunes truncates s to at most maxRunes runes.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) == 0 {
		return ""
	}

	runeCount := 0
	for idx := range s {
		if runeCount == maxRunes {
			return s[:idx]
		}
		runeCount++
	}

	return s
}
