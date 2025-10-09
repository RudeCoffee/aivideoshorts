package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

// TranscriptSegment represents a single segment of the video transcript.
type TranscriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// UploadResponse is the structure of the JSON response sent after a video upload.
type UploadResponse struct {
	Transcript  []TranscriptSegment `json:"transcript"`
	VideoFile   string              `json:"videoFile"`
}

func main() {
	// Check if ffmpeg is installed.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("ffmpeg is not installed or not in the system's PATH.")
	}

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "templates/index.html")
	})
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/clip", clipHandler)

	log.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 32 MB is the default used by FormFile
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Failed to parse multipart form", http.StatusInternalServerError)
		return
	}

	file, handler, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "Invalid video file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create the uploads directory if it doesn't exist
	if _, err := os.Stat("uploads"); os.IsNotExist(err) {
		os.Mkdir("uploads", os.ModeDir)
	}

	// Create a new file in the uploads directory
	dst, err := os.Create(filepath.Join("uploads", handler.Filename))
	if err != nil {
		http.Error(w, "Failed to create file on server", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the uploaded file to the destination file
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save file on server", http.StatusInternalServerError)
		return
	}

	transcript, err := transcribeVideo(dst.Name())
	if err != nil {
		http.Error(w, "Failed to transcribe video", http.StatusInternalServerError)
		return
	}

	response := UploadResponse{
		Transcript: transcript,
		VideoFile:  filepath.Base(dst.Name()),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func clipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	start := r.FormValue("start")
	end := r.FormValue("end")
	videoFile := r.FormValue("videoFile")

	if start == "" || end == "" || videoFile == "" {
		http.Error(w, "Missing required form values: start, end, and videoFile", http.StatusBadRequest)
		return
	}

	// Create a unique name for the clip
	clipFile := fmt.Sprintf("clip-%s-%s-%s", start, end, videoFile)
	clipPath := filepath.Join("static", clipFile)

	cmd := exec.Command("ffmpeg", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-to", end, "-c", "copy", clipPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/static/%s", clipFile)
}

func transcribeVideo(videoPath string) ([]TranscriptSegment, error) {
	// In a real application, you would use a transcription service.
	// For this example, we'll return a dummy transcript.
	log.Printf("Transcribing video: %s", videoPath)
	transcript := []TranscriptSegment{
		{Start: 0.5, End: 2.0, Text: "This is the first sentence."},
		{Start: 2.5, End: 5.0, Text: "This is the second sentence, which is a bit longer."},
		{Start: 5.5, End: 8.0, Text: "And here is a third and final sentence."},
	}
	return transcript, nil
}