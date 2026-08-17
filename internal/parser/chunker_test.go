package parser

import (
	"fmt"
	"strings"
	"testing"
)

func numberedWords(count int) []string {
	words := make([]string, count)
	for i := range words {
		words[i] = fmt.Sprintf("w%03d", i)
	}
	return words
}

// An empty or whitespace-only body yields no chunks. notes.embedNote and
// notes.Reindex both rely on that empty result to substitute the note title,
// so an empty note still gets one searchable chunk. If ChunkText ever
// returned a blank chunk instead, those notes would be indexed as empty text
// and become unfindable.
func TestChunkTextEmptyBodyYieldsNoChunks(t *testing.T) {
	for _, body := range []string{"", "   ", "\n\n\t \r\n"} {
		if chunks := ChunkText(body, 200, 40); len(chunks) != 0 {
			t.Errorf("ChunkText(%q) = %q, want no chunks", body, chunks)
		}
	}
}

// A body shorter than one chunk stays a single chunk with its markdown
// formatting intact — retrieval shows chunk text back to the user.
func TestChunkTextShortBodyStaysOneChunk(t *testing.T) {
	body := "\n## Heading\n\nA short note body.\n"
	chunks := ChunkText(body, 200, 40)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0] != strings.TrimSpace(body) {
		t.Errorf("got %q, want %q", chunks[0], strings.TrimSpace(body))
	}
}

// Consecutive chunks must share exactly overlapWords, and stripping that
// shared prefix must reassemble the original text — no word dropped at a
// boundary, none duplicated beyond the intended overlap.
func TestChunkTextOverlapsWithoutDroppingContent(t *testing.T) {
	cases := []struct {
		wordCount     int
		wordsPerChunk int
		overlapWords  int
	}{
		{100, 10, 4},
		{100, 200, 40}, // production settings, body longer than one chunk
		{250, 200, 40},
		{41, 10, 9},  // maximum overlap, step of 1
		{31, 10, 0},  // no overlap at all
		{101, 10, 4}, // final chunk is a partial
	}
	for _, testCase := range cases {
		name := fmt.Sprintf("%dwords_%dper_%doverlap", testCase.wordCount, testCase.wordsPerChunk, testCase.overlapWords)
		t.Run(name, func(t *testing.T) {
			words := numberedWords(testCase.wordCount)
			chunks := ChunkText(strings.Join(words, " "), testCase.wordsPerChunk, testCase.overlapWords)
			if len(chunks) == 0 {
				t.Fatal("got no chunks")
			}

			reassembled := strings.Fields(chunks[0])
			for i := 1; i < len(chunks); i++ {
				previous := strings.Fields(chunks[i-1])
				current := strings.Fields(chunks[i])
				if len(previous) < testCase.overlapWords || len(current) < testCase.overlapWords {
					t.Fatalf("chunks %d/%d too short to carry %d words of overlap: %q / %q",
						i-1, i, testCase.overlapWords, chunks[i-1], chunks[i])
				}
				shared := previous[len(previous)-testCase.overlapWords:]
				if strings.Join(shared, " ") != strings.Join(current[:testCase.overlapWords], " ") {
					t.Fatalf("chunk %d does not repeat the last %d words of chunk %d: %v vs %v",
						i, testCase.overlapWords, i-1, current[:testCase.overlapWords], shared)
				}
				reassembled = append(reassembled, current[testCase.overlapWords:]...)
			}

			if strings.Join(reassembled, " ") != strings.Join(words, " ") {
				t.Errorf("reassembled %d words, want the original %d:\ngot  %v\nwant %v",
					len(reassembled), len(words), reassembled, words)
			}
		})
	}
}

// A body longer than one chunk really is split; otherwise the overlap
// assertions above would pass vacuously on a single chunk.
func TestChunkTextSplitsLongBody(t *testing.T) {
	chunks := ChunkText(strings.Join(numberedWords(1000), " "), 200, 40)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks for 1000 words at 200 per chunk, want several", len(chunks))
	}
	for i, chunk := range chunks {
		if got := len(strings.Fields(chunk)); got > 200 {
			t.Errorf("chunk %d has %d words, want at most 200", i, got)
		}
	}
}

// An overlap at or above the chunk size is bad config. It must terminate and
// still cover the whole body rather than loop forever emitting the same words.
func TestChunkTextOverlapNotSmallerThanChunkTerminates(t *testing.T) {
	for _, overlap := range []int{5, 9, 100} {
		words := numberedWords(23)
		chunks := ChunkText(strings.Join(words, " "), 5, overlap)
		if len(chunks) == 0 {
			t.Fatalf("overlap %d: got no chunks", overlap)
		}
		joined := strings.Join(chunks, " ")
		for _, word := range words {
			if !strings.Contains(joined, word) {
				t.Fatalf("overlap %d: %q missing from chunks %q", overlap, word, chunks)
			}
		}
	}
}

// Markdown bodies are whitespace-heavy. Chunking counts words, so blank lines
// and indentation must not create empty chunks that get embedded as nothing.
func TestChunkTextProducesNoBlankChunks(t *testing.T) {
	body := "# Title\n\n\n\n" + strings.Repeat("word\n\n", 300)
	for _, chunk := range ChunkText(body, 200, 40) {
		if strings.TrimSpace(chunk) == "" {
			t.Fatal("got a blank chunk")
		}
	}
}
