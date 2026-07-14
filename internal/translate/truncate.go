package translate

// MaxTranslateRawBytes is the max raw changelog size sent to the model.
// Larger inputs are truncated with a note; full raw is retained by the caller/store.
const MaxTranslateRawBytes = 12000

const truncateNote = "\n\n…(truncated for translation, full raw retained)"

// TruncateForTranslate returns raw unchanged when within the byte budget.
// Otherwise it keeps a rune-safe prefix up to MaxTranslateRawBytes and appends a note.
// The result may exceed MaxTranslateRawBytes because of the suffix note.
func TruncateForTranslate(raw string) string {
	if len(raw) <= MaxTranslateRawBytes {
		return raw
	}
	// Accumulate complete runes until the next rune would exceed the byte budget.
	n := 0
	for _, r := range raw {
		size := len(string(r))
		if n+size > MaxTranslateRawBytes {
			break
		}
		n += size
	}
	return raw[:n] + truncateNote
}
