package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	pigo "github.com/esimov/pigo/core"
)

func movingAverage(data []int, windowSize int) []int {
	if windowSize <= 1 {
		return data
	}
	smoothed := make([]int, len(data))
	for i := range data {
		start := i - windowSize/2
		if start < 0 {
			start = 0
		}
		end := i + windowSize/2
		if end > len(data) {
			end = len(data)
		}
		sum := 0
		for _, val := range data[start:end] {
			sum += val
		}
		smoothed[i] = sum / (end - start)
	}
	return smoothed
}

type videoDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func getVideoDimensions(videoPath string) (*videoDimensions, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "json", videoPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffprobe error: %s\n%s", err, output)
	}

	var data struct {
		Streams []videoDimensions `json:"streams"`
	}
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, err
	}

	if len(data.Streams) == 0 {
		return nil, fmt.Errorf("no video streams found")
	}

	return &data.Streams[0], nil
}

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
	http.HandleFunc("/autoclip", autoClipHandler)

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

func autoClipHandler(w http.ResponseWriter, r *http.Request) {
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
	clipFile := fmt.Sprintf("autoclip-%s-%s-%s", start, end, videoFile)
	clipPath := filepath.Join("static", clipFile)

	// Get video dimensions
	dims, err := getVideoDimensions(filepath.Join("uploads", videoFile))
	if err != nil {
		log.Printf("Failed to get video dimensions: %s", err)
		http.Error(w, "Failed to get video dimensions", http.StatusInternalServerError)
		return
	}

	// Create a temporary directory to store frames
	framesDir := filepath.Join("uploads", "frames")
	if err := os.MkdirAll(framesDir, os.ModePerm); err != nil {
		http.Error(w, "Failed to create frames directory", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(framesDir)

	// Extract frames from the video
	cmdFrames := exec.Command("ffmpeg", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-to", end, filepath.Join(framesDir, "frame-%04d.png"))
	output, err := cmdFrames.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to extract frames: %s", output), http.StatusInternalServerError)
		return
	}

	cascadeFile, err := os.ReadFile(filepath.Join("cascade", "facefinder"))
	if err != nil {
		log.Println("Cascade file not found, downloading...")
		err := os.MkdirAll("cascade", os.ModePerm)
		if err != nil {
			http.Error(w, "Failed to create cascade directory", http.StatusInternalServerError)
			return
		}
		url := "https://github.com/esimov/pigo/raw/master/cascade/facefinder"
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("Failed to download cascade file: %s", err)
			http.Error(w, "Failed to download cascade file", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		out, err := os.Create(filepath.Join("cascade", "facefinder"))
		if err != nil {
			log.Printf("Failed to create cascade file: %s", err)
			http.Error(w, "Failed to create cascade file", http.StatusInternalServerError)
			return
		}
		defer out.Close()
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			log.Printf("Failed to save cascade file: %s", err)
			http.Error(w, "Failed to save cascade file", http.StatusInternalServerError)
			return
		}
		cascadeFile, err = os.ReadFile(filepath.Join("cascade", "facefinder"))
		if err != nil {
			log.Printf("Failed to read cascade file after download: %s", err)
			http.Error(w, "Failed to read cascade file", http.StatusInternalServerError)
			return
		}
	}

	var facePositions []int
	frameFiles, err := filepath.Glob(filepath.Join(framesDir, "*.png"))
	if err != nil {
		log.Printf("Failed to find frame files: %s", err)
	}

	p := pigo.NewPigo()
	// Unpack the binary file. This will return the number of cascade trees,
	// the tree depth, the threshold and the prediction from tree's leaf nodes.
	classifier, err := p.Unpack(cascadeFile)
	if err != nil {
		log.Fatalf("Error reading the cascade file: %s", err)
	}

	for _, file := range frameFiles {
		src, err := pigo.GetImage(filepath.ToSlash(file))
		if err != nil {
			log.Printf("Cannot open the image file: %v", err)
			continue
		}

		pixels := pigo.RgbToGrayscale(src)
		cols, rows := src.Bounds().Max.X, src.Bounds().Max.Y

		cParams := pigo.CascadeParams{
			MinSize:     20,
			MaxSize:     1000,
			ShiftFactor: 0.1,
			ScaleFactor: 1.1,

			ImageParams: pigo.ImageParams{
				Pixels: pixels,
				Rows:   rows,
				Cols:   cols,
				Dim:    cols,
			},
		}

		// Run the classifier over the obtained leaf nodes and return the detection results.
		// The result contains quadruplets representing the row, column, scale and detection score.
		detections := classifier.RunCascade(cParams, 0.0)

		// Calculate the intersection over union (IoU) of two clusters.
		detections = classifier.ClusterDetections(detections, 0.2)

		if len(detections) > 0 {
			facePositions = append(facePositions, detections[0].Col)
		} else if len(facePositions) > 0 {
			// If no face is detected, use the last known position
			facePositions = append(facePositions, facePositions[len(facePositions)-1])
		} else {
			// If no face has been detected yet, use the center of the frame
			facePositions = append(facePositions, dims.Width/2)
		}
	}

	// Smooth the face positions using a simple moving average
	smoothedPositions := movingAverage(facePositions, 15)

	// Generate the sendcmd file in the root directory
	sendcmdPath := "sendcmd.txt"
	cmdFile, err := os.Create(sendcmdPath)
	if err != nil {
		log.Printf("Failed to create sendcmd file: %s", err)
		http.Error(w, "Failed to create sendcmd file", http.StatusInternalServerError)
		return
	}
	defer os.Remove(sendcmdPath) // Clean up the command file

	cropWidth := dims.Height * 9 / 16
	for i, pos := range smoothedPositions {
		x := pos - (cropWidth / 2)
		if x < 0 {
			x = 0
		}
		if x+cropWidth > dims.Width {
			x = dims.Width - cropWidth
		}
		// The time for the command is based on the frame number and the video's frame rate (assuming 30fps)
		frameTime := float64(i) / 30.0
		// Add semicolon and use CRLF line endings for Windows compatibility
		cmdFile.WriteString(fmt.Sprintf("%f crop x %d;\r\n", frameTime, x))
	}
	cmdFile.Close() // Close the file to ensure it's written before ffmpeg reads it

	// Set initial crop x value and use forward slashes in the path for cross-platform compatibility
	vf := fmt.Sprintf("sendcmd=f=%s,crop=w=%d:h=%d:x=0,scale=1080:1920,setsar=1", sendcmdPath, cropWidth, dims.Height)
	cmd := exec.Command("ffmpeg", "-i", filepath.Join("uploads", videoFile), "-vf", vf, "-ss", start, "-to", end, clipPath)

	log.Printf("Executing ffmpeg command: %s", cmd.String())
	output, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/static/%s", clipFile)
}

func parseVTTTimestamp(ts string) (float64, error) {
	// 00:00:00.000 or 00:00.000 format
	ts = strings.Replace(ts, ",", ".", 1)
	parts := strings.Split(ts, ":")
	var hours, minutes, seconds float64
	var err error

	if len(parts) == 3 {
		hours, err = strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		minutes, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
		seconds, err = strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return 0, err
		}
	} else if len(parts) == 2 {
		minutes, err = strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		seconds, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
	} else {
		return 0, fmt.Errorf("invalid timestamp format: %s", ts)
	}

	return hours*3600 + minutes*60 + seconds, nil
}

func parseVTT(vttPath string) ([]TranscriptSegment, error) {
	file, err := os.Open(vttPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var segments []TranscriptSegment
	var currentSegment *TranscriptSegment

	// Skip WEBVTT header
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			break
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "-->") {
			parts := strings.Split(line, " --> ")
			if len(parts) == 2 {
				start, err1 := parseVTTTimestamp(strings.Split(parts[0], " ")[0])
				end, err2 := parseVTTTimestamp(strings.Split(parts[1], " ")[0])
				if err1 == nil && err2 == nil {
					currentSegment = &TranscriptSegment{Start: start, End: end}
				}
			}
		} else if currentSegment != nil && line != "" {
			currentSegment.Text = line
			segments = append(segments, *currentSegment)
			currentSegment = nil // Reset for the next segment
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return segments, nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func transcribeVideo(videoPath string) ([]TranscriptSegment, error) {
	log.Printf("Starting transcription for video: %s", videoPath)

	// 1. Extract audio to a temporary WAV file.
	audioPath := filepath.Join("uploads", "temp_audio.wav")
	cmdAudio := exec.Command("ffmpeg", "-i", videoPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-y", audioPath)
	output, err := cmdAudio.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg audio extraction error: %s\n%s", err, output)
		return nil, fmt.Errorf("failed to extract audio: %s", output)
	}
	defer os.Remove(audioPath)

	// 2. Run whisper.cpp to transcribe the audio and output to a VTT file.
	var whisperCliPath string
	if runtime.GOOS == "windows" {
		defaultPath := filepath.Join(".", "whisper.cpp", "build", "bin", "Release", "whisper-cli.exe")
		whisperCliPath = getEnv("WHISPER_CLI_PATH", defaultPath)
	} else {
		defaultPath := filepath.Join(".", "whisper.cpp", "build", "bin", "whisper-cli")
		whisperCliPath = getEnv("WHISPER_CLI_PATH", defaultPath)
	}

	defaultModelPath := filepath.Join(".", "whisper.cpp", "models", "ggml-base.en.bin")
	whisperModelPath := getEnv("WHISPER_MODEL_PATH", defaultModelPath)
	transcriptOutputPath := filepath.Join("uploads", "temp_transcript")

	// Prepare arguments for whisper-cli
	args := []string{
		"-m", whisperModelPath,
		"-f", audioPath,
		"-ovtt", // Output in VTT format
		"-of", transcriptOutputPath,
	}

	// Check for GPU layers environment variable to enable GPU acceleration
	if gpuLayers, ok := os.LookupEnv("WHISPER_GPU_LAYERS"); ok {
		args = append(args, "--n-gpu-layers", gpuLayers)
		log.Printf("GPU acceleration enabled with %s layers.", gpuLayers)
	}

	cmdWhisper := exec.Command(whisperCliPath, args...)
	cmdWhisper.Dir = "." // Run from the app's root directory

	output, err = cmdWhisper.CombinedOutput()
	if err != nil {
		log.Printf("whisper.cpp error: %s\n%s", err, output)
		return nil, fmt.Errorf("failed to transcribe audio: %s", output)
	}

	// The tool appends .vtt to the output file name.
	transcriptVTTPath := transcriptOutputPath + ".vtt"
	defer os.Remove(transcriptVTTPath)

	// 3. Read and parse the transcript VTT file.
	transcript, err := parseVTT(transcriptVTTPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VTT transcript: %w", err)
	}

	log.Printf("Transcription successful for video: %s", videoPath)
	return transcript, nil
}