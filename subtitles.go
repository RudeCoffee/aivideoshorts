package main

import (
	"fmt"
	"strings"
	"time"
)

// AssStyle defines the look of the subtitles
const AssHeader = `[Script Info]
ScriptType: v4.00+
PlayResX: 1080
PlayResY: 1920

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,60,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,3,0,2,10,10,250,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`

// generateAssFile converts VTT transcript segments into an ASS file content string
func generateAssFromTranscript(segments []TranscriptSegment) string {
	var sb strings.Builder
	sb.WriteString(AssHeader)

	for _, seg := range segments {
		// Clean text
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}

		// Split into smaller chunks (Shorts style)
		chunks := chunkText(text, 4) // max 4 words per chunk

		segmentDuration := seg.End - seg.Start
		totalWords := len(strings.Fields(text))

		currentStart := seg.Start

		for _, chunk := range chunks {
			chunkWords := len(strings.Fields(chunk))
			chunkDuration := segmentDuration * (float64(chunkWords) / float64(totalWords))
			currentEnd := currentStart + chunkDuration

			// Apply styling
			styledText := styleChunk(chunk)

			// Format time to ASS format: H:MM:SS.cs
			startStr := formatAssTime(currentStart)
			endStr := formatAssTime(currentEnd)

			// Append event
			// Dialogue: 0,0:00:00.00,0:00:05.00,Default,,0,0,0,,Text
			sb.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", startStr, endStr, styledText))

			currentStart = currentEnd
		}
	}
	return sb.String()
}

func chunkText(text string, maxWords int) []string {
	words := strings.Fields(text)
	var chunks []string

	for i := 0; i < len(words); i += maxWords {
		end := i + maxWords
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
	}
	return chunks
}

func styleChunk(text string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	// Find the longest word to highlight
	longestIdx := -1
	maxLength := 0

	for i, w := range words {
		cleanW := strings.Trim(w, ".,!?:;\"'")
		if len(cleanW) > maxLength {
			maxLength = len(cleanW)
			longestIdx = i
		}
	}

	var styledWords []string
	for i, w := range words {
		if i == longestIdx {
			// Highlight yellow: &H00FFFF& (BGR)
			styledWords = append(styledWords, fmt.Sprintf("{\\c&H00FFFF&}%s{\\c&HFFFFFF&}", w))
		} else {
			styledWords = append(styledWords, w)
		}
	}

	// Join back and add fade effect
	// \fad(100,100) -> fade in 100ms, fade out 100ms
	return fmt.Sprintf("{\\fad(100,100)}%s", strings.Join(styledWords, " "))
}

func formatAssTime(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	cs := int(d.Milliseconds() / 10) % 100 // centiseconds

	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}
