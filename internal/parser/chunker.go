package parser

import "strings"

// ChunkText splits body into overlapping word-based chunks. Overlap keeps
// context from being severed right at a chunk boundary (e.g. a sentence
// that explains something gets some of the explanation in both chunks).
func ChunkText(body string, wordsPerChunk, overlapWords int) []string {
	words := strings.Fields(body)
	if len(words) == 0 {
		return nil
	}
	if len(words) <= wordsPerChunk {
		return []string{strings.TrimSpace(body)}
	}

	var chunks []string
	step := wordsPerChunk - overlapWords
	if step <= 0 {
		step = wordsPerChunk // guard against bad config causing an infinite loop
	}

	for start := 0; start < len(words); start += step {
		end := start + wordsPerChunk
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}
