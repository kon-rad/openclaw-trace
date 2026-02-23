package ingest

func TruncateBytes(input string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	raw := []byte(input)
	if len(raw) <= maxBytes {
		return input
	}
	return string(raw[:maxBytes])
}
