package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	_ "image"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

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
	Transcript []TranscriptSegment `json:"transcript"`
	VideoFile  string              `json:"videoFile"`
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
	http.HandleFunc("/extract-frame", extractFrameHandler)

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
	cropX := r.FormValue("cropX")
	cropY := r.FormValue("cropY")
	cropWidth := r.FormValue("cropWidth")
	cropHeight := r.FormValue("cropHeight")

	if start == "" || end == "" || videoFile == "" {
		http.Error(w, "Missing required form values: start, end, and videoFile", http.StatusBadRequest)
		return
	}

	// Create a unique name for the clip
	clipFile := fmt.Sprintf("clip-%s-%s-%s", start, end, videoFile)
	clipPath := filepath.Join("static", clipFile)

	var cmd *exec.Cmd
	if cropWidth != "" && cropHeight != "" && cropX != "" && cropY != "" {
		// Manual crop
		vf_string := fmt.Sprintf("crop=%s:%s:%s:%s,scale=1080:1920,setsar=1", cropWidth, cropHeight, cropX, cropY)
		cmd = exec.Command("ffmpeg", "-y", "-i", filepath.Join("uploads", videoFile), "-vf", vf_string, "-c:a", "aac", "-af", "loudnorm", "-ss", start, "-to", end, clipPath)
	} else {
		// No crop, just cut
		cmd = exec.Command("ffmpeg", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-to", end, "-c:v", "copy", "-c:a", "aac", "-af", "loudnorm", clipPath)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/static/%s", clipFile)
}

func extractFrameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	videoFile := r.FormValue("videoFile")
	start := r.FormValue("start")

	if videoFile == "" || start == "" {
		http.Error(w, "Missing required form values: videoFile and start", http.StatusBadRequest)
		return
	}

	frameDir := filepath.Join("static", "frames")
	if _, err := os.Stat(frameDir); os.IsNotExist(err) {
		os.Mkdir(frameDir, os.ModeDir)
	}

	framePath := filepath.Join(frameDir, fmt.Sprintf("frame-%s.png", videoFile))
	cmd := exec.Command("ffmpeg", "-y", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-vframes", "1", framePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error extracting frame: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to extract frame: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/%s", filepath.ToSlash(framePath))
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

	// Create a temporary directory to store the first frame
	frameDir := filepath.Join("uploads", "first_frame")
	if err := os.MkdirAll(frameDir, os.ModePerm); err != nil {
		http.Error(w, "Failed to create frame directory", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(frameDir)

	// Extract the first frame of the clip
	firstFramePath := filepath.Join(frameDir, "first_frame.png")
	cmdFrame := exec.Command("ffmpeg", "-y", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-vframes", "1", firstFramePath)
	output, err := cmdFrame.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error extracting first frame: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to extract first frame: %s", output), http.StatusInternalServerError)
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

	p := pigo.NewPigo()
	// Unpack the binary file. This will return the number of cascade trees,
	// the tree depth, the threshold and the prediction from tree's leaf nodes.
	classifier, err := p.Unpack(cascadeFile)
	if err != nil {
		log.Fatalf("Error reading the cascade file: %s", err)
	}

	src, err := pigo.GetImage(filepath.ToSlash(firstFramePath))
	if err != nil {
		log.Printf("Cannot open the image file: %v", err)
	}

	// Default to a centered crop
	cropWidth := dims.Height * 9 / 16
	cropX := (dims.Width - cropWidth) / 2

	if err == nil { // if pigo.GetImage was successful
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
		detections := classifier.RunCascade(cParams, 0.0)
		// Calculate the intersection over union (IoU) of two clusters.
		detections = classifier.ClusterDetections(detections, 0.2)

		if len(detections) > 0 {
			// Center the crop on the first detected face
			faceX := detections[0].Col
			cropX = faceX - (cropWidth / 2)

			// Clamp cropX to ensure it's within video bounds
			if cropX < 0 {
				cropX = 0
			}
			if cropX+cropWidth > dims.Width {
				cropX = dims.Width - cropWidth
			}
		}
	}

	subtitlePath := filepath.ToSlash(filepath.Join("uploads", strings.TrimSuffix(videoFile, filepath.Ext(videoFile))+".vtt"))
	vf_string := fmt.Sprintf("crop=%d:%d:%d:0,scale=1080:1920,setsar=1,subtitles=%s:force_style='Alignment=2\\,FontName=Arial\\,FontSize=18\\,PrimaryColour=&Hffffff\\,BackColor=&H80000000\\,BorderStyle=1\\,Outline=1\\,Shadow=0\\,MarginV=50'", cropWidth, dims.Height, cropX, strings.ReplaceAll(subtitlePath, "\\", "/"))
	cmd := exec.Command("ffmpeg", "-y", "-i", filepath.Join("uploads", videoFile), "-vf", vf_string, "-c:a", "aac", "-af", "loudnorm", "-ss", start, "-to", end, clipPath)
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
	transcriptOutputPath := filepath.Join("uploads", strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath)))

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

	// 3. Read and parse the transcript VTT file.
	transcript, err := parseVTT(transcriptVTTPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VTT transcript: %w", err)
	}

	log.Printf("Transcription successful for video: %s", videoPath)
	return transcript, nil
}
