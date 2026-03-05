package cmd

func truncateRunesWithEllipsis(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) == 0 {
		return ""
	}
	if maxRunes <= 3 {
		return truncateRunes(s, maxRunes)
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

func truncateRunes(s string, maxRunes int) string {
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
